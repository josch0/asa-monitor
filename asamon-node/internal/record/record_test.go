// SPDX-License-Identifier: GPL-3.0-or-later

package record

import (
	"bufio"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestJederTypWirdGelesen(t *testing.T) {
	zeilen := []string{
		`{"type":"init","seq":0,"ts":"2026-08-26T14:03:11.482913771Z","format_version":1,"channel":"5C","freq_hz":178352000,"device":"rtl_sdr","device_serial":"","rx_version":"0.1.0","rx_commit":"abc1234","welle_commit":"fe06fad"}`,
		`{"type":"tlm","seq":1,"ts":"2026-08-26T14:03:12Z","snr":12.4,"sync":true,"signal":true,"freq_corr":{"fine":-3,"coarse":0},"fib_total":125,"fib_crc_err":2,"dropped":0,"parse_errors":0,"eid":"0x10FF","ens_time":"2026-08-26T14:03:11Z","ens_offset_min":60}`,
		`{"type":"ens","seq":2,"ts":"2026-08-26T14:03:13Z","eid":"0x10FF","ecc":224,"label":"Bundesmux 1","services":[{"sid":"0x0D3110AB","label":"ASA DE","components":[{"subch_id":7,"start_addr":128,"size":48,"protection":"EEP 2-A","bitrate":32}]}]}`,
		`{"type":"asa","seq":3,"ts":"2026-08-26T14:03:14Z","heartbeat":true,"cn":true,"oe":false,"pd_second_half":false,"raw":"018f"}`,
		`{"type":"aud","seq":4,"ts":"2026-08-26T14:03:15Z","subch_id":7,"alert_uid":"uid1",` +
			`"dir":"/var/lib/asamon/audio","started":"2026-08-26T14:03:00Z","seconds":15.5,` +
			`"truncated":false,"sample_rate":48000,"channels":2,"mode":"HE-AACv2",` +
			`"files":[{"name":"uid1-5C-7.dabp","codec":"dabp","bytes":76800,"sha256":"ab"},` +
			`{"name":"uid1-5C-7.mp3","codec":"mp3","bytes":65536,"sha256":"cd"}]}`,
	}
	r := NewReader(strings.NewReader(strings.Join(zeilen, "\n") + "\n"))
	gelesen := slices.Collect(r.Alle())
	if err := r.Err(); err != nil {
		t.Fatalf("Strom riss ab: %v", err)
	}
	if len(gelesen) != 5 {
		t.Fatalf("%d Records gelesen, erwartet 5", len(gelesen))
	}

	rec := gelesen[0]
	if rec.Kind != KindInit {
		t.Fatalf("init: %v", rec.Kind)
	}
	if rec.Init.Channel != "5C" || rec.Init.FormatVersion != 1 || rec.Init.FreqHz != 178352000 {
		t.Errorf("init: %+v", rec.Init)
	}
	if rec.Ts != "2026-08-26T14:03:11.482913771Z" {
		t.Errorf("ts wurde verändert: %q", rec.Ts)
	}
	if rec.Zeit.Nanosecond() != 482913771 {
		t.Errorf("die Nanosekunden gingen verloren: %v", rec.Zeit)
	}

	rec = gelesen[1]
	if rec.Kind != KindTlm {
		t.Fatalf("tlm: %v", rec.Kind)
	}
	if rec.Tlm.Snr == nil || *rec.Tlm.Snr != 12.4 {
		t.Errorf("snr: %+v", rec.Tlm.Snr)
	}
	if rec.Tlm.FibTotal != 125 || rec.Tlm.FibCrcErr != 2 || rec.Tlm.Eid != "0x10FF" {
		t.Errorf("tlm: %+v", rec.Tlm)
	}
	if rec.Tlm.FreqCorr.Fine != -3 {
		t.Errorf("freq_corr: %+v", rec.Tlm.FreqCorr)
	}

	rec = gelesen[2]
	if rec.Kind != KindEns {
		t.Fatalf("ens: %v", rec.Kind)
	}
	if len(rec.Ens.Services) != 1 || len(rec.Ens.Services[0].Komponenten) != 1 {
		t.Fatalf("ens: %+v", rec.Ens)
	}
	if k := rec.Ens.Services[0].Komponenten[0]; k.SubChID != 7 || k.Bitrate != 32 || k.Protection != "EEP 2-A" {
		t.Errorf("Komponente: %+v", k)
	}

	rec = gelesen[3]
	if rec.Kind != KindAsa {
		t.Fatalf("asa: %v", rec.Kind)
	}
	if !rec.Asa.Heartbeat || !rec.Asa.Cn || rec.Asa.Raw != "018f" {
		t.Errorf("asa: %+v", rec.Asa)
	}
	if rec.Asa.SubChID != nil {
		t.Error("ein Heartbeat hat kein subch_id — der Zeiger müsste nil sein")
	}

	rec = gelesen[4]
	if rec.Kind != KindAud {
		t.Fatalf("aud: %v", rec.Kind)
	}
	if rec.Aud.SubChID != 7 || rec.Aud.AlertUID != "uid1" || rec.Aud.Seconds != 15.5 {
		t.Errorf("aud: %+v", rec.Aud)
	}
	if len(rec.Aud.Files) != 2 {
		t.Fatalf("aud: %d Dateien statt 2", len(rec.Aud.Files))
	}
	if rec.Aud.Files[0].Codec != "dabp" || rec.Aud.Files[0].Bytes != 76800 ||
		rec.Aud.Files[1].Codec != "mp3" || rec.Aud.Files[1].Name != "uid1-5C-7.mp3" {
		t.Errorf("aud-Dateien: %+v", rec.Aud.Files)
	}

	if z := r.Zaehler(); z.Zeilen != 5 || z.SeqLuecken != 0 || z.KaputteZeilen != 0 {
		t.Errorf("Zähler: %+v", z)
	}
}

// Die Fixtures aus asamon-rx sind die Nahtstelle zwischen beiden Programmen:
// Sie halten byteweise fest, was der Empfangsprozess schreibt. Wer sie hier
// nicht mehr lesen kann, hat das Format gebrochen.
func TestFixturesAusAsamonRx(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "..", "testdata", "streams", "fig0_15.fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	gelesen := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "expect=") {
			continue
		}
		rec, err := ParseLine([]byte(strings.TrimPrefix(line, "expect=")))
		if err != nil {
			t.Errorf("%s: %v", line, err)
			continue
		}
		if rec.Kind != KindAsa {
			t.Errorf("%s: Kind ist %s", line, rec.Kind)
			continue
		}
		if rec.Asa.Raw == "" {
			t.Errorf("%s: raw fehlt — es ist der Beleg und nie optional", line)
		}
		gelesen++
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if gelesen < 15 {
		t.Errorf("nur %d Fixtures gelesen; die Datei hat mehr", gelesen)
	}
}

func TestFelderDerFixturesImEinzelnen(t *testing.T) {
	cases := []struct {
		name  string
		zeile string
		pruef func(*testing.T, *Asa)
	}{
		{"heartbeat", `{"type":"asa","seq":0,"ts":"1970-01-01T00:00:00Z","heartbeat":true,"cn":true,"oe":false,"pd_second_half":false,"raw":"018f"}`,
			func(t *testing.T, a *Asa) {
				if !a.Heartbeat || a.Phase != "" || a.Nff != nil {
					t.Errorf("%+v", a)
				}
			}},
		{"trigger_level1_start", `{"type":"asa","seq":0,"ts":"1970-01-01T00:00:00Z","heartbeat":false,"cn":false,"oe":false,"pd_second_half":false,"phase":"trigger","subch_id":7,"stage":"level1_start","iid":3,"last":true,"nff":0,"location_codes":"0a2b3c4d","raw":"070f47830a2b3c4d"}`,
			func(t *testing.T, a *Asa) {
				if a.Phase != "trigger" || a.SubChID == nil || *a.SubChID != 7 {
					t.Errorf("%+v", a)
				}
				if a.Stage != "level1_start" || a.Iid == nil || *a.Iid != 3 || a.Last == nil || !*a.Last {
					t.Errorf("Status-Feld: %+v", a)
				}
				if a.Nff == nil || *a.Nff != 0 || a.LocationCodes != "0a2b3c4d" {
					t.Errorf("Location Codes: %+v", a)
				}
			}},
		{"pre_trigger_sec63", `{"type":"asa","seq":0,"ts":"1970-01-01T00:00:00Z","heartbeat":false,"cn":false,"oe":false,"pd_second_half":false,"phase":"pre_trigger","subch_id":12,"sec":63,"stage":"level2_start","iid":9,"last":false,"raw":"040f0c3f49"}`,
			func(t *testing.T, a *Asa) {
				if a.Sec == nil || *a.Sec != 63 {
					t.Errorf("sec: %+v", a.Sec)
				}
			}},
		{"oe_trigger", `{"type":"asa","seq":0,"ts":"1970-01-01T00:00:00Z","heartbeat":false,"cn":false,"oe":true,"pd_second_half":false,"other_eid":"0x10FF","stage":"level1_repeat","iid":5,"last":true,"nff":0,"location_codes":"0102","raw":"064f10ffa50102"}`,
			func(t *testing.T, a *Asa) {
				if !a.Oe || a.OtherEid != "0x10FF" {
					t.Errorf("%+v", a)
				}
				// Ein OE-Alert hat kein Phasenfeld — OE-Signalisierung ist
				// nach TS 104 089 §6.5.1 stets Trigger.
				if a.Phase != "" || a.SubChID != nil {
					t.Errorf("OE-Record mit Phase/SubChId: %+v", a)
				}
			}},
		{"sustain", `{"type":"asa","seq":0,"ts":"1970-01-01T00:00:00Z","heartbeat":false,"cn":true,"oe":false,"pd_second_half":false,"phase":"sustain","subch_id":7,"raw":"028f87"}`,
			func(t *testing.T, a *Asa) {
				// Bei Sustain besteht das Type-0-Feld nur aus dem Id-Feld.
				if a.Stage != "" || a.Iid != nil || a.Last != nil {
					t.Errorf("Sustain mit Status-Feld: %+v", a)
				}
			}},
		{"stage_test", `{"type":"asa","seq":0,"ts":"1970-01-01T00:00:00Z","heartbeat":false,"cn":false,"oe":false,"pd_second_half":false,"phase":"trigger","subch_id":4,"stage":"test","iid":0,"last":false,"raw":"030f4470"}`,
			func(t *testing.T, a *Asa) {
				if a.Stage != "test" {
					t.Errorf("%+v", a)
				}
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec, err := ParseLine([]byte(c.zeile))
			if err != nil {
				t.Fatal(err)
			}
			c.pruef(t, rec.Asa)
		})
	}
}

func TestSeqLueckenWerdenGezaehlt(t *testing.T) {
	zeilen := `{"type":"tlm","seq":10,"ts":"2026-08-26T14:03:12Z"}
{"type":"tlm","seq":11,"ts":"2026-08-26T14:03:13Z"}
{"type":"tlm","seq":15,"ts":"2026-08-26T14:03:14Z"}
{"type":"tlm","seq":16,"ts":"2026-08-26T14:03:15Z"}
{"type":"tlm","seq":12,"ts":"2026-08-26T14:03:16Z"}
`
	r := NewReader(strings.NewReader(zeilen))
	for range r.Alle() {
	}
	z := r.Zaehler()
	if z.SeqLuecken != 3 {
		t.Errorf("SeqLuecken ist %d, erwartet 3 (12, 13, 14)", z.SeqLuecken)
	}
	if z.SeqRueckwaerts != 1 {
		t.Errorf("SeqRueckwaerts ist %d, erwartet 1", z.SeqRueckwaerts)
	}
}

func TestUnbekanntesWirdGezaehltNichtVerworfen(t *testing.T) {
	zeilen := `{"type":"tlm","seq":0,"ts":"2026-08-26T14:03:12Z","fib_total":125}
{"type":"kuenftig","seq":1,"ts":"2026-08-26T14:03:13Z","was":"auch immer"}
{"type":"tlm","seq":2,"ts":"2026-08-26T14:03:14Z","fib_total":125,"neues_feld":42}
kein JSON
{"type":"tlm"
{}
{"type":"tlm","seq":3,"ts":"2026-08-26T14:03:15Z"}
`
	r := NewReader(strings.NewReader(zeilen))
	var typen []Kind
	for rec := range r.Alle() {
		typen = append(typen, rec.Kind)
	}
	if len(typen) != 4 {
		t.Fatalf("%d Records gelesen: %v", len(typen), typen)
	}
	if typen[1] != KindUnbekannt {
		t.Errorf("der unbekannte Typ kam als %s", typen[1])
	}
	z := r.Zaehler()
	if z.UnbekannteRecords != 1 {
		t.Errorf("UnbekannteRecords ist %d, erwartet 1", z.UnbekannteRecords)
	}
	if z.KaputteZeilen != 3 {
		t.Errorf("KaputteZeilen ist %d, erwartet 3", z.KaputteZeilen)
	}
	// Das unbekannte Feld im dritten Record darf nichts kaputt gemacht haben.
	if typen[2] != KindTlm {
		t.Errorf("der Record mit unbekanntem Feld kam als %s", typen[2])
	}
}

func TestLangeZeilenReissenDenStromNicht(t *testing.T) {
	// Seit die Audiobytes nicht mehr durch den Strom gehen, ist der längste
	// Record ein ens mit vielen Services — deutlich kürzer als das hier. Der
	// Fall bleibt trotzdem geprüft: Ein Ensemble, das sich seltsam meldet,
	// darf den Strom nicht abreißen.
	daten := strings.Repeat("A", 400000) // 400 kB in einem einzigen Feld
	zeile := `{"type":"ens","seq":0,"ts":"2026-08-26T14:03:12Z","eid":"0x10BC","label":"` + daten + `"}`
	r := NewReader(strings.NewReader(zeile + "\n" + `{"type":"tlm","seq":1,"ts":"2026-08-26T14:03:13Z"}` + "\n"))

	lang := slices.Collect(r.Alle())
	if err := r.Err(); err != nil {
		t.Fatalf("die lange Zeile riss den Strom ab: %v", err)
	}
	if len(lang) != 2 {
		t.Fatalf("%d Records gelesen", len(lang))
	}
	if lang[0].Kind != KindEns || len(lang[0].Ens.Label) != len(daten) {
		t.Errorf("die lange Zeile kam verstümmelt an: %d Zeichen im Label", len(lang[0].Ens.Label))
	}
	if lang[1].Kind != KindTlm {
		t.Errorf("nach der langen Zeile: %v", lang[1].Kind)
	}
}

func TestNeustartBeginntNeueZaehlung(t *testing.T) {
	// Nach einem Neustart von asamon-rx beginnt seq wieder bei 0. Der Leser
	// eines *neuen* Stroms darf das nicht als Lücke sehen — deshalb bekommt
	// jeder Strom seinen eigenen Reader.
	erster := NewReader(strings.NewReader(`{"type":"tlm","seq":998,"ts":"2026-08-26T14:03:12Z"}` + "\n"))
	if n := len(slices.Collect(erster.Alle())); n != 1 {
		t.Fatalf("%d Records im ersten Strom", n)
	}
	zweiter := NewReader(strings.NewReader(`{"type":"init","seq":0,"ts":"2026-08-26T14:03:20Z","channel":"5C"}` + "\n"))
	if n := len(slices.Collect(zweiter.Alle())); n != 1 {
		t.Fatalf("%d Records im zweiten Strom", n)
	}
	if z := zweiter.Zaehler(); z.SeqLuecken != 0 || z.SeqRueckwaerts != 0 {
		t.Errorf("der neue Strom meldet Lücken: %+v", z)
	}
}

func FuzzParseLine(f *testing.F) {
	f.Add(`{"type":"asa","seq":0,"ts":"1970-01-01T00:00:00Z","heartbeat":true,"cn":true,"oe":false,"pd_second_half":false,"raw":"018f"}`)
	f.Add(`{"type":"tlm","seq":1,"ts":"x","snr":null}`)
	f.Add(`{"type":"aud","seq":2,"ts":"","subch_id":7,"alert_uid":"u","files":[{"name":"a.dabp","codec":"dabp","bytes":1,"sha256":"x"}]}`)
	f.Add(`{}`)
	f.Add(``)
	f.Fuzz(func(t *testing.T, line string) {
		rec, err := ParseLine([]byte(line))
		if err != nil {
			return
		}
		// Was der Parser annimmt, muss in sich stimmig sein: genau eine
		// Nutzlast, und die passend zum Typ.
		nutzlasten := 0
		for _, gesetzt := range []bool{rec.Init != nil, rec.Tlm != nil, rec.Ens != nil, rec.Asa != nil, rec.Aud != nil} {
			if gesetzt {
				nutzlasten++
			}
		}
		if rec.Kind == KindUnbekannt {
			if nutzlasten != 0 {
				t.Fatalf("unbekannter Typ %q mit Nutzlast", rec.TypeRaw)
			}
			return
		}
		if nutzlasten != 1 {
			t.Fatalf("%s hat %d Nutzlasten", rec.Kind, nutzlasten)
		}
		_ = rec.Kind.String()
	})
}

// Was encoding/json/v2 (Go 1.27) am Strom ändert — und was davon Absicht ist.
//
// Der Record-Strom ist ein Beleg. Wo eine Zeile mehrdeutig ist, soll sie als
// kaputt gezählt werden statt stillschweigend halb gedeutet; wo sie nur
// verstümmelt ist, soll der Rest erhalten bleiben. Diese drei Fälle halten die
// Grenze fest.
func TestStrengeDerDeutung(t *testing.T) {
	// Doppelte Feldnamen sind mehrdeutig. Die alte Fassung nahm den letzten
	// Wert; das ist eine Deutung, für die es keine Grundlage gibt.
	doppelt := `{"type":"asa","seq":1,"ts":"2026-08-26T14:03:11Z","type":"tlm","raw":"018f"}`
	if _, err := ParseLine([]byte(doppelt)); err == nil {
		t.Error("eine Zeile mit zwei type-Feldern wurde angenommen")
	}

	// Auch tiefer im Objekt.
	doppeltTief := `{"type":"asa","seq":1,"ts":"2026-08-26T14:03:11Z","raw":"018f","raw":"0000"}`
	if _, err := ParseLine([]byte(doppeltTief)); err == nil {
		t.Error("eine Zeile mit zwei raw-Feldern wurde angenommen")
	}

	// Die Feldnamen stehen in docs/record-format.md klein fest. Ein Sender,
	// der davon abweicht, soll auffallen und nicht halb verstanden werden.
	gross := `{"Type":"asa","Seq":1,"Ts":"2026-08-26T14:03:11Z","Raw":"018f"}`
	if _, err := ParseLine([]byte(gross)); err == nil {
		t.Error("großgeschriebene Feldnamen wurden angenommen")
	}

	// Ungültiges UTF-8 dagegen ist ein Empfangssymptom, kein Formatfehler:
	// Ein verstümmeltes Ensemble-Label darf nicht den ganzen ens-Record kosten.
	// Ohne ihn gäbe es keinen ens_hash und damit auch keinen asa_hash.
	verstuemmelt := "{\"type\":\"ens\",\"seq\":2,\"ts\":\"2026-08-26T14:03:11Z\"," +
		"\"eid\":\"0x10FF\",\"ecc\":224,\"label\":\"Bundes\xff mux\"}"
	rec, err := ParseLine([]byte(verstuemmelt))
	if err != nil {
		t.Fatalf("ein verstümmeltes Label kostete den ganzen Record: %v", err)
	}
	if rec.Ens.Eid != "0x10FF" || rec.Ens.Ecc != 224 {
		t.Errorf("die übrigen Felder gingen verloren: %+v", rec.Ens)
	}
	if rec.Ens.Label == "" {
		t.Error("das Label wurde ganz verworfen statt ersetzt")
	}

	// Und unbekannte Felder bleiben erlaubt: Das Format darf additiv wachsen.
	kuenftig := `{"type":"asa","seq":3,"ts":"2026-08-26T14:03:11Z","raw":"018f","kuenftiges_feld":42}`
	if _, err := ParseLine([]byte(kuenftig)); err != nil {
		t.Errorf("ein unbekanntes Feld wurde als Fehler gewertet: %v", err)
	}
}

// Der Alarmfall ist der Lastfall: bis zu 12 asa-Records je Sekunde, dazu die
// aud-Records mit mehreren Kilobyte Base64 — und das auf einem Pi.
//
// Die Zahlen begründen die Wahl von encoding/json/v2 in ParseLine.
func BenchmarkParseLineAsa(b *testing.B) {
	zeile := []byte(`{"type":"asa","seq":42,"ts":"2026-08-26T14:03:11.482913771Z","heartbeat":false,` +
		`"cn":false,"oe":false,"pd_second_half":false,"phase":"trigger","subch_id":7,` +
		`"stage":"level1_start","iid":3,"last":true,"nff":0,"location_codes":"0a2b3c4d",` +
		`"raw":"070f47830a2b3c4d"}`)
	b.SetBytes(int64(len(zeile)))
	for b.Loop() {
		if _, err := ParseLine(zeile); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseLineAud(b *testing.B) {
	// Seit dem 30.08.2026 ist das ein kleiner Record am Ende der Aufnahme
	// statt 4 kB Base64 je Sekunde — der teuerste Pfad dieses Parsers ist
	// damit entfallen. Der Vergleich bleibt stehen, weil er genau das zeigt.
	zeile := []byte(`{"type":"aud","seq":812,"ts":"2026-08-26T14:03:11.482913771Z",` +
		`"subch_id":13,"alert_uid":"7c2dabcd","dir":"/var/lib/asamon/audio",` +
		`"started":"2026-08-30T12:14:55.000000000Z","seconds":43.75,"truncated":false,` +
		`"sample_rate":48000,"channels":2,"mode":"HE-AACv2","mp3_bitrate":64,` +
		`"files":[{"name":"7c2dabcd-5C-13.dabp","codec":"dabp","bytes":262144,"sha256":"aa"},` +
		`{"name":"7c2dabcd-5C-13.mp3","codec":"mp3","bytes":245760,"sha256":"bb"}]}`)
	b.SetBytes(int64(len(zeile)))
	for b.Loop() {
		if _, err := ParseLine(zeile); err != nil {
			b.Fatal(err)
		}
	}
}
