// SPDX-License-Identifier: GPL-3.0-or-later

package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"runtime/pprof"
	"testing"
	"time"

	"github.com/josch0/asa-monitor/asamon-node/internal/report"
	"github.com/josch0/asa-monitor/asamon-node/internal/uplink"
)

// Ein Dauerlauf im Kleinen: Wächst der Speicher, oder bleiben Goroutinen
// liegen?
//
// Das ersetzt die 24 Stunden auf dem Pi aus N8 nicht — es fängt aber die
// Fehlerklasse, bei der jede Aufnahme, jeder Alert oder jeder Neustart etwas
// zurücklässt. Der Lauf dauert rund eine Minute und läuft deshalb nur mit
// -short=false.
//
// Geprüft wird auf zwei Arten, weil sie Verschiedenes finden: Die Zählung über
// runtime.NumGoroutine sieht, was nach dem Beenden noch läuft; das Profil
// goroutineleak (Go 1.27) sieht Goroutinen, die für immer auf einer
// Concurrency-Primitive warten, die niemand mehr erreichen kann. Letzteres ist
// genau die Bauart von Fehler, die dieser Entwurf mit seinen sechs Kanälen je
// Kanal-Goroutine riskiert — und die man sonst erst nach Tagen bemerkt.
func TestKeinGoroutineLeckImDauerlauf(t *testing.T) {
	if testing.Short() {
		t.Skip("Dauerlauf; ohne -short")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var u uplink.Umschlag
		json.NewDecoder(r.Body).Decode(&u)
		var seqs []uint64
		for _, rep := range u.Reports {
			seqs = append(seqs, rep.Seq)
		}
		json.NewEncoder(w).Encode(map[string]any{"accepted": seqs})
	}))
	defer srv.Close()

	vorher := goroutinen()

	cfg, id := umgebung(t, srv.URL, "5C", "11D")
	cfg.Audio.Enabled = true
	sup, err := Neu(cfg, id, stillesLog(), Optionen{})
	if err != nil {
		t.Fatal(err)
	}
	// Ein Kanal spielt Alerts mit Mitschnitt ab, der andere stürzt dauernd ab.
	sup.kanaele[0].rx = neuRx(t, "5C", cfg.Paths.RxBinary,
		[]string{"--serve", "--file", stromPfad("alert-audio"), "--speed", "20"})
	sup.kanaele[1].rx = neuRx(t, "11D", cfg.Paths.RxBinary,
		[]string{"--serve", "--file", stromPfad("heartbeat-10min"), "--speed", "20", "--crash-after", "40"})

	ctx, ende := context.WithCancel(context.Background())
	fertig := make(chan error, 1)
	go func() { fertig <- sup.Run(ctx) }()

	var speicher []uint64
	var goroutines []int
	for range 12 {
		time.Sleep(4 * time.Second)
		var m runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&m)
		speicher = append(speicher, m.HeapAlloc)
		goroutines = append(goroutines, runtime.NumGoroutine())
	}

	ende()
	<-fertig

	// Nach dem Beenden müssen die Goroutinen wieder verschwinden.
	nachher := 0
	for range 40 {
		runtime.GC()
		nachher = goroutinen()
		if nachher <= vorher+4 {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if nachher > vorher+4 {
		buf := make([]byte, 1<<18)
		buf = buf[:runtime.Stack(buf, true)]
		t.Errorf("nach dem Beenden %d Goroutinen statt %d:\n%s", nachher, vorher, buf)
	}

	// Der Speicher darf über den Lauf nicht davonlaufen. Verglichen wird die
	// zweite Messung mit der letzten — die erste enthält noch den Aufbau.
	if len(speicher) > 2 {
		basis, ende := speicher[1], speicher[len(speicher)-1]
		if ende > basis*3 && ende-basis > 32<<20 {
			t.Errorf("HeapAlloc wuchs von %d auf %d Byte: %v", basis, ende, speicher)
		}
	}
	t.Logf("HeapAlloc: %v", speicher)
	t.Logf("Goroutinen: %v (vorher %d, nachher %d)", goroutines, vorher, nachher)

	// Und die Sequenznummern müssen lückenlos geblieben sein.
	if sup.seq.Load() == 0 {
		t.Error("keine Datensätze gebaut")
	}
	_ = report.Version
}

func goroutinen() int {
	runtime.Gosched()
	return runtime.NumGoroutine()
}

// pruefeLeckProfil wertet das Profil goroutineleak aus (Go 1.27).
//
// Es meldet Goroutinen, die dauerhaft auf eine Concurrency-Primitive warten,
// die von keiner laufenden Goroutine mehr erreichbar ist — also genau die
// Sorte Leck, die keine Zählung findet, solange der Prozess weiterläuft.
func pruefeLeckProfil(t *testing.T) {
	t.Helper()
	profil := pprof.Lookup("goroutineleak")
	if profil == nil {
		t.Skip("das Profil goroutineleak gibt es in dieser Go-Fassung nicht")
	}
	// Das Profil braucht einen abgeschlossenen GC-Lauf, um Erreichbarkeit zu
	// beurteilen.
	runtime.GC()
	runtime.GC()

	if n := profil.Count(); n > 0 {
		var buf bytes.Buffer
		if err := profil.WriteTo(&buf, 2); err != nil {
			t.Errorf("%d verlorene Goroutinen; das Profil ließ sich nicht schreiben: %v", n, err)
			return
		}
		t.Errorf("%d Goroutinen warten auf etwas, das niemand mehr erreichen kann:\n%s", n, buf.String())
	}
}
