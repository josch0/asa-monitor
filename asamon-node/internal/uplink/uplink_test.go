// SPDX-License-Identifier: GPL-3.0-or-later

package uplink

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/josch0/asa-monitor/asamon-node/internal/report"
)

func stillesLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// testServer baut eine Gegenstelle auf einem echten Port.
//
// httptest.NewTestServer (Go 1.27) böte dafür ein In-Memory-Netz, und das wäre
// schneller und ohne Firewall-Nachfrage. Es setzt aber voraus, dass der
// Prüfling seinen http.Client von Server.Client() bezieht — der Uplink baut
// seinen Transport bewusst selbst (eigene Timeouts, MaxIdleConnsPerHost, kein
// http.DefaultClient), und genau diese Einstellungen sollen die Tests
// mitdurchlaufen. Ein untergeschobener Client prüfte sie nicht mehr.
func testServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func datensatz(seq uint64) *report.Report {
	return &report.Report{ReportVersion: 1, Seq: seq, Trigger: report.TriggerInterval}
}

func neuGegen(url string) *Uplink {
	return Neu(Konfig{BaseURL: url, Timeout: 2 * time.Second, NodeID: "test-node"}, stillesLog())
}

func TestUmschlagUndAntwort(t *testing.T) {
	var gesehen struct {
		pfad, typ, node string
		seqs            []uint64
	}
	srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		gesehen.pfad = r.URL.Path
		gesehen.typ = r.Header.Get("Content-Type")
		gesehen.node = r.Header.Get("X-Asamon-Node")
		var u Umschlag
		if err := json.NewDecoder(r.Body).Decode(&u); err != nil {
			t.Error(err)
		}
		for _, rep := range u.Reports {
			gesehen.seqs = append(gesehen.seqs, rep.Seq)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"accepted":     []uint64{1, 2},
			"duplicates":   []uint64{3},
			"rejected":     []map[string]any{{"seq": 4, "reason": "report_version unsupported"}},
			"audio_wanted": []string{"7c2d"},
			"server_time":  time.Now().UTC().Format(time.RFC3339Nano),
		})
	})

	u := neuGegen(srv.URL)
	a, err := u.Sende(context.Background(), []*report.Report{datensatz(1), datensatz(2), datensatz(3), datensatz(4)})
	if err != nil {
		t.Fatal(err)
	}
	if gesehen.pfad != PfadReports {
		t.Errorf("Pfad ist %q, erwartet %q", gesehen.pfad, PfadReports)
	}
	if gesehen.typ != "application/json" {
		t.Errorf("Content-Type ist %q", gesehen.typ)
	}
	if gesehen.node != "test-node" {
		t.Errorf("X-Asamon-Node ist %q", gesehen.node)
	}
	if len(gesehen.seqs) != 4 {
		t.Errorf("%d Datensätze kamen an", len(gesehen.seqs))
	}
	// Angenommen und Duplikat bedeuten dasselbe: Der Server hat sie.
	erledigt := a.Erledigt()
	if len(erledigt) != 3 {
		t.Errorf("Erledigt() gab %v, erwartet drei (2 accepted + 1 duplicate)", erledigt)
	}
	if len(a.Rejected) != 1 || a.Rejected[0].Seq != 4 {
		t.Errorf("Rejected: %+v", a.Rejected)
	}
	if len(a.AudioWanted) != 1 || a.AudioWanted[0] != "7c2d" {
		t.Errorf("AudioWanted: %v", a.AudioWanted)
	}
}

// Der Umschlag ist auch bei einem einzelnen Datensatz eine Liste — ein Format
// statt zwei.
func TestEinzelnerDatensatzGehtAlsListe(t *testing.T) {
	var rumpf string
	srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		rumpf = string(raw)
		w.Write([]byte(`{"accepted":[1]}`))
	})

	if _, err := neuGegen(srv.URL).Sende(context.Background(), []*report.Report{datensatz(1)}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rumpf, `{"reports":[`) {
		t.Errorf("Rumpf ist kein Umschlag mit Liste: %.60s", rumpf)
	}
}

func TestWiederholungNurWoSieSinnHat(t *testing.T) {
	cases := []struct {
		status      int
		wiederholen bool
	}{
		{http.StatusInternalServerError, true},
		{http.StatusBadGateway, true},
		{http.StatusServiceUnavailable, true},
		{http.StatusRequestTimeout, true},
		{http.StatusTooManyRequests, true},
		{http.StatusBadRequest, false},
		{http.StatusNotFound, false},
		{http.StatusUnprocessableEntity, false},
	}
	for _, c := range cases {
		srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
			if c.status == http.StatusTooManyRequests {
				w.Header().Set("Retry-After", "42")
			}
			w.WriteHeader(c.status)
			w.Write([]byte("nein"))
		})
		_, err := neuGegen(srv.URL).Sende(context.Background(), []*report.Report{datensatz(1)})

		if err == nil {
			t.Errorf("HTTP %d wurde als Erfolg gewertet", c.status)
			continue
		}
		f, ok := err.(*Fehler)
		if !ok {
			t.Errorf("HTTP %d: %v ist kein *Fehler", c.status, err)
			continue
		}
		if f.Wiederholen != c.wiederholen {
			t.Errorf("HTTP %d: Wiederholen=%v, erwartet %v", c.status, f.Wiederholen, c.wiederholen)
		}
		if c.status == http.StatusTooManyRequests && f.RetryAfter != 42*time.Second {
			t.Errorf("Retry-After wurde nicht übernommen: %s", f.RetryAfter)
		}
	}
}

func TestNetzfehlerUndTimeoutWerdenWiederholt(t *testing.T) {
	// Ein Server, der nie antwortet.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
	}))
	defer srv.Close()

	u := Neu(Konfig{BaseURL: srv.URL, Timeout: 200 * time.Millisecond}, stillesLog())
	_, err := u.Sende(context.Background(), []*report.Report{datensatz(1)})
	if err == nil {
		t.Fatal("das Timeout wurde nicht bemerkt")
	}
	if f, ok := err.(*Fehler); !ok || !f.Wiederholen {
		t.Errorf("ein Timeout muss wiederholbar sein: %v", err)
	}

	// Und ein Rechner, den es nicht gibt.
	u = Neu(Konfig{BaseURL: "http://127.0.0.1:1", Timeout: time.Second}, stillesLog())
	_, err = u.Sende(context.Background(), []*report.Report{datensatz(1)})
	if f, ok := err.(*Fehler); !ok || !f.Wiederholen {
		t.Errorf("ein Netzfehler muss wiederholbar sein: %v", err)
	}
}

func TestBackoffWaechstUndStreut(t *testing.T) {
	u := neuGegen("http://example.invalid")
	var vorher time.Duration
	for i := range 12 {
		d := u.Backoff()
		if d <= 0 {
			t.Fatalf("Backoff %d ist %s", i, d)
		}
		if float64(d) > float64(BackoffMax)*(1+BackoffJitter) {
			t.Errorf("Backoff %d ist %s und damit über der Obergrenze", i, d)
		}
		if i > 0 && i < 8 && d < vorher {
			// Die Streuung darf die Verdopplung nicht umkehren.
			t.Errorf("Backoff %d (%s) ist kleiner als der vorige (%s)", i, d, vorher)
		}
		vorher = d
	}
	u.BackoffZuruecksetzen()
	if d := u.Backoff(); d > 2*BackoffStart {
		t.Errorf("nach dem Zurücksetzen ist der Backoff %s", d)
	}

	// Zwei Knoten dürfen nicht im Gleichtakt wiederkommen.
	a, b := neuGegen("http://x"), neuGegen("http://x")
	gleich := 0
	for range 20 {
		if a.Backoff() == b.Backoff() {
			gleich++
		}
		a.BackoffZuruecksetzen()
		b.BackoffZuruecksetzen()
	}
	if gleich > 2 {
		t.Errorf("%d von 20 Wartezeiten waren identisch — die Streuung wirkt nicht", gleich)
	}
}

func TestIdempotenzUeberSeq(t *testing.T) {
	var aufrufe atomic.Int32
	gesehen := map[uint64]int{}
	srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		aufrufe.Add(1)
		var u Umschlag
		json.NewDecoder(r.Body).Decode(&u)
		var accepted, duplicates []uint64
		for _, rep := range u.Reports {
			if gesehen[rep.Seq] > 0 {
				duplicates = append(duplicates, rep.Seq)
			} else {
				accepted = append(accepted, rep.Seq)
			}
			gesehen[rep.Seq]++
		}
		json.NewEncoder(w).Encode(map[string]any{"accepted": accepted, "duplicates": duplicates})
	})

	u := neuGegen(srv.URL)
	stapel := []*report.Report{datensatz(1), datensatz(2)}
	erste, err := u.Sende(context.Background(), stapel)
	if err != nil {
		t.Fatal(err)
	}
	if len(erste.Accepted) != 2 || len(erste.Duplicates) != 0 {
		t.Errorf("erster Versand: %+v", erste)
	}
	zweite, err := u.Sende(context.Background(), stapel)
	if err != nil {
		t.Fatal(err)
	}
	if len(zweite.Duplicates) != 2 {
		t.Errorf("zweiter Versand: %+v", zweite)
	}
	// Ein zweites Mal geliefert heißt duplicates — das ist ein Erfolg, kein
	// Fehler, und der Datensatz verlässt den Spool.
	if len(zweite.Erledigt()) != 2 {
		t.Errorf("Duplikate gelten nicht als erledigt: %v", zweite.Erledigt())
	}
}

func TestAudioUpload(t *testing.T) {
	var kopf http.Header
	var rumpf []byte
	var pfad string
	srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		pfad = r.URL.Path
		kopf = r.Header.Clone()
		rumpf, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	})

	daten := []byte("roher subchannel-bitstrom")
	u := neuGegen(srv.URL)
	err := u.LadeAudio(context.Background(), "7c2dabcd", AudioKopf{
		Channel: "5C", SubChID: 7, Started: "2026-08-26T14:03:00.000Z",
		Sha256: "deadbeef", Truncated: true,
	}, strings.NewReader(string(daten)), int64(len(daten)))
	if err != nil {
		t.Fatal(err)
	}
	if pfad != "/api/v1/alerts/7c2dabcd/audio" {
		t.Errorf("Pfad ist %q", pfad)
	}
	if string(rumpf) != string(daten) {
		t.Errorf("der Rumpf wurde verändert: %q", rumpf)
	}
	if kopf.Get("Content-Type") != "application/octet-stream" {
		t.Errorf("Content-Type ist %q", kopf.Get("Content-Type"))
	}
	for name, want := range map[string]string{
		"X-Asamon-Node":      "test-node",
		"X-Asamon-Channel":   "5C",
		"X-Asamon-SubChId":   "7",
		"X-Asamon-Started":   "2026-08-26T14:03:00.000Z",
		"X-Asamon-Sha256":    "deadbeef",
		"X-Asamon-Truncated": "true",
	} {
		if got := kopf.Get(name); got != want {
			t.Errorf("%s ist %q, erwartet %q", name, got, want)
		}
	}
}

// 201 angenommen, 200 hatten wir schon, 413 zu groß — in allen drei Fällen
// gilt die Datei als erledigt.
func TestAudioAntwortenGeltenAlsErledigt(t *testing.T) {
	for _, status := range []int{http.StatusCreated, http.StatusOK, http.StatusRequestEntityTooLarge} {
		srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
			io.Copy(io.Discard, r.Body)
			w.WriteHeader(status)
		})
		err := neuGegen(srv.URL).LadeAudio(context.Background(), "uid", AudioKopf{Channel: "5C"},
			strings.NewReader("x"), 1)
		if err != nil {
			t.Errorf("HTTP %d gilt nicht als erledigt: %v", status, err)
		}
	}

	srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusInternalServerError)
	})
	err := neuGegen(srv.URL).LadeAudio(context.Background(), "uid", AudioKopf{Channel: "5C"},
		strings.NewReader("x"), 1)
	if err == nil {
		t.Error("HTTP 500 beim Audio-Upload wurde als Erfolg gewertet")
	}
}

func TestUnlesbareAntwort(t *testing.T) {
	srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("kein JSON"))
	})
	_, err := neuGegen(srv.URL).Sende(context.Background(), []*report.Report{datensatz(1)})
	if err == nil {
		t.Fatal("eine unlesbare Antwort wurde angenommen")
	}
	if f, ok := err.(*Fehler); !ok || !f.Wiederholen {
		t.Errorf("eine unlesbare Antwort sollte wiederholbar sein: %v", err)
	}
}

func TestRetryAfterFormate(t *testing.T) {
	if got := retryAfter("30"); got != 30*time.Second {
		t.Errorf("retryAfter(\"30\") = %s", got)
	}
	if got := retryAfter(time.Now().Add(time.Minute).UTC().Format(http.TimeFormat)); got < 50*time.Second {
		t.Errorf("retryAfter mit Datum = %s", got)
	}
	if got := retryAfter("Unsinn"); got != 0 {
		t.Errorf("retryAfter(\"Unsinn\") = %s", got)
	}
	if got := retryAfter(""); got != 0 {
		t.Errorf("retryAfter(\"\") = %s", got)
	}
}
