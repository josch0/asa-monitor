// SPDX-License-Identifier: GPL-3.0-or-later

// Paket uplink schickt die Datensätze zum Server und lädt Audio hoch, wenn der
// Server es anfordert.
//
// Das Vertragsdokument zur Serverseite ist docs/uplink-protokoll.md. Eine
// Authentifizierung gibt es bewusst nicht; der Knoten führt trotzdem ein
// Ed25519-Schlüsselpaar mit, damit Signieren später ohne Identitätswechsel
// nachrüstbar bleibt.
package uplink

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/josch0/asa-monitor/asamon-node/internal/report"
)

const (
	// PfadReports und PfadAudio sind die beiden Endpunkte.
	PfadReports = "/api/v1/reports"
	PfadAudio   = "/api/v1/alerts/%s/audio"

	// BackoffStart und BackoffMax spannen die Wiederholung auf.
	BackoffStart = time.Second
	BackoffMax   = 300 * time.Second

	// BackoffJitter ist der Zufallsanteil, mit dem die Wartezeit gestreut
	// wird. Ohne ihn kämen nach einem Serverausfall alle Knoten des Netzes im
	// Gleichtakt wieder — und legten ihn erneut lahm.
	BackoffJitter = 0.2
)

// Umschlag ist der Rumpf von POST /api/v1/reports. Auch ein einzelner
// Datensatz geht als Liste — ein Format statt zwei.
type Umschlag struct {
	Reports []*report.Report `json:"reports"`
}

// Antwort ist die Auskunft des Servers.
type Antwort struct {
	Accepted   []uint64 `json:"accepted"`
	Duplicates []uint64 `json:"duplicates"`
	Rejected   []struct {
		Seq    uint64 `json:"seq"`
		Reason string `json:"reason"`
	} `json:"rejected"`
	// AudioWanted nennt die alert_uids, zu denen der Server noch kein Audio
	// hat. Das ist der Kern der Crowd-Ersparnis: Hat ein anderer Knoten
	// schneller hochgeladen, spart dieser den Upload. Ohne diese Liste lädt
	// der Knoten **nichts** hoch.
	AudioWanted []string `json:"audio_wanted"`
	ServerTime  string   `json:"server_time"`
}

// Erledigt gibt die Sequenznummern, die der Server hat — angenommen oder als
// Duplikat erkannt. Beides heißt: Der Datensatz darf aus dem Spool.
func (a *Antwort) Erledigt() []uint64 {
	out := make([]uint64, 0, len(a.Accepted)+len(a.Duplicates))
	out = append(out, a.Accepted...)
	return append(out, a.Duplicates...)
}

// Konfig ist die Einstellung des Uplinks.
type Konfig struct {
	BaseURL            string
	Timeout            time.Duration
	InsecureSkipVerify bool
	NodeID             string
}

// Fehler unterscheidet, ob eine Wiederholung Sinn hat.
type Fehler struct {
	Status      int
	Nachricht   string
	Wiederholen bool
	// RetryAfter ist die Angabe aus der gleichnamigen Kopfzeile, falls sie kam.
	RetryAfter time.Duration
}

func (e *Fehler) Error() string {
	if e.Status > 0 {
		return fmt.Sprintf("HTTP %d: %s", e.Status, e.Nachricht)
	}
	return e.Nachricht
}

// Uplink ist der HTTP-Zugang zum Server.
type Uplink struct {
	k      Konfig
	client *http.Client
	log    *slog.Logger

	backoff time.Duration
}

// Neu baut den Uplink.
//
// Kein http.DefaultClient: Der hat kein Timeout, und eine hängende Anfrage
// blockierte den Versand dauerhaft.
func Neu(k Konfig, log *slog.Logger) *Uplink {
	if k.Timeout <= 0 {
		k.Timeout = 15 * time.Second
	}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          4,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	if k.InsecureSkipVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &Uplink{
		k:       k,
		client:  &http.Client{Timeout: k.Timeout, Transport: transport},
		log:     log,
		backoff: BackoffStart,
	}
}

// Sende schickt eine Reihe von Datensätzen.
func (u *Uplink) Sende(ctx context.Context, berichte []*report.Report) (*Antwort, error) {
	if len(berichte) == 0 {
		return &Antwort{}, nil
	}
	rumpf, err := json.Marshal(Umschlag{Reports: berichte})
	if err != nil {
		return nil, &Fehler{Nachricht: "Datensätze serialisieren: " + err.Error()}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(u.k.BaseURL, "/")+PfadReports, bytes.NewReader(rumpf))
	if err != nil {
		return nil, &Fehler{Nachricht: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "asamon-node")
	req.Header.Set("X-Asamon-Node", u.k.NodeID)

	res, err := u.client.Do(req)
	if err != nil {
		// Netzfehler und Timeouts sind vorübergehend: wiederholen.
		return nil, &Fehler{Nachricht: err.Error(), Wiederholen: true}
	}
	defer res.Body.Close()

	roh, leseFehler := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		return nil, u.statusFehler(res, roh)
	}
	if leseFehler != nil {
		return nil, &Fehler{Status: res.StatusCode, Nachricht: leseFehler.Error(), Wiederholen: true}
	}

	var antwort Antwort
	if err := json.Unmarshal(roh, &antwort); err != nil {
		return nil, &Fehler{Status: res.StatusCode, Nachricht: "Antwort ist kein gültiges JSON: " + err.Error(), Wiederholen: true}
	}
	u.pruefeServerzeit(antwort.ServerTime)
	for _, r := range antwort.Rejected {
		u.log.Error("der Server hat einen Datensatz abgelehnt", "seq", r.Seq, "grund", r.Reason)
	}
	u.backoff = BackoffStart
	return &antwort, nil
}

// statusFehler entscheidet, ob wiederholt wird.
//
// 4xx außer 408 und 429 wird **nicht** wiederholt: Ein dauerhaft abgelehnter
// Datensatz darf den Spool nicht füllen.
func (u *Uplink) statusFehler(res *http.Response, rumpf []byte) *Fehler {
	f := &Fehler{Status: res.StatusCode, Nachricht: kurzeMeldung(rumpf)}
	switch {
	case res.StatusCode >= 500:
		f.Wiederholen = true
	case res.StatusCode == http.StatusRequestTimeout:
		f.Wiederholen = true
	case res.StatusCode == http.StatusTooManyRequests:
		f.Wiederholen = true
		f.RetryAfter = retryAfter(res.Header.Get("Retry-After"))
	}
	return f
}

// LadeAudio schickt eine Mitschnittdatei.
//
// Der Rumpf ist der rohe Subchannel-Bitstrom, unverändert wie empfangen — kein
// AAC, kein Umkodieren, keine Superframe-Zerlegung.
func (u *Uplink) LadeAudio(ctx context.Context, alertUID string, kopf AudioKopf, daten io.Reader, laenge int64) error {
	ziel := strings.TrimRight(u.k.BaseURL, "/") + fmt.Sprintf(PfadAudio, alertUID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ziel, daten)
	if err != nil {
		return &Fehler{Nachricht: err.Error()}
	}
	req.ContentLength = laenge
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("User-Agent", "asamon-node")
	req.Header.Set("X-Asamon-Node", u.k.NodeID)
	req.Header.Set("X-Asamon-Channel", kopf.Channel)
	req.Header.Set("X-Asamon-SubChId", strconv.Itoa(kopf.SubChID))
	req.Header.Set("X-Asamon-Started", kopf.Started)
	req.Header.Set("X-Asamon-Sha256", kopf.Sha256)
	req.Header.Set("X-Asamon-Truncated", strconv.FormatBool(kopf.Truncated))

	res, err := u.client.Do(req)
	if err != nil {
		return &Fehler{Nachricht: err.Error(), Wiederholen: true}
	}
	defer res.Body.Close()
	roh, _ := io.ReadAll(io.LimitReader(res.Body, 64*1024))

	switch res.StatusCode {
	case http.StatusCreated, http.StatusOK, http.StatusRequestEntityTooLarge:
		// 201 angenommen, 200 hatten wir schon, 413 zu groß. In allen drei
		// Fällen gilt die Datei als erledigt.
		if res.StatusCode == http.StatusRequestEntityTooLarge {
			u.log.Warn("der Server hat den Mitschnitt als zu groß abgelehnt", "alert_uid", alertUID)
		}
		return nil
	default:
		return u.statusFehler(res, roh)
	}
}

// AudioKopf sind die Metadaten, die als Kopfzeilen mitgehen.
type AudioKopf struct {
	Channel   string
	SubChID   int
	Started   string
	Sha256    string
	Truncated bool
}

// Backoff gibt die nächste Wartezeit und schreibt sie fort.
//
// Exponentiell 1 s → 2 → 4 … max 300 s, mit ±20 % Streuung.
func (u *Uplink) Backoff() time.Duration {
	d := u.backoff
	u.backoff = min(u.backoff*2, BackoffMax)
	return streue(d)
}

// BackoffZuruecksetzen setzt die Wartezeit nach einem Erfolg zurück.
func (u *Uplink) BackoffZuruecksetzen() { u.backoff = BackoffStart }

func streue(d time.Duration) time.Duration {
	spanne := float64(d) * BackoffJitter
	return time.Duration(float64(d) + (rand.Float64()*2-1)*spanne)
}

// pruefeServerzeit meldet, wenn die Uhren auseinanderlaufen. Die Uhr ist für
// diesen Knoten Voraussetzung, nicht Zubehör.
func (u *Uplink) pruefeServerzeit(s string) {
	if s == "" {
		return
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return
	}
	if abweichung := time.Since(t); abweichung > 5*time.Second || abweichung < -5*time.Second {
		u.log.Warn("die Knotenuhr weicht deutlich von der Serverzeit ab",
			"abweichung", abweichung.Round(time.Millisecond),
			"hinweis", "läuft chrony oder systemd-timesyncd?")
	}
}

func retryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if sek, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && sek >= 0 {
		return time.Duration(sek) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func kurzeMeldung(rumpf []byte) string {
	s := strings.TrimSpace(string(rumpf))
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 200 {
		return s[:200] + "…"
	}
	if s == "" {
		return "(keine Meldung)"
	}
	return s
}
