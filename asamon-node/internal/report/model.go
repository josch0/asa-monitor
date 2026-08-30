// SPDX-License-Identifier: GPL-3.0-or-later

// Paket report ist das Datenmodell des Datensatzes, den der Knoten zum Server
// schickt. Die verbindliche Beschreibung steht in docs/uplink-protokoll.md.
//
// Ein Datensatz je report_interval (Vorgabe 10 s), **immer**, auch wenn nichts
// empfangen wurde. Sonst kann der Server "Ensemble sendet keinen Heartbeat"
// nicht von "Knoten ist tot" unterscheiden — und damit wäre die
// Abdeckungskarte wertlos.
//
// Das Paket enthält nur Daten und ihre Darstellung. Wer sie füllt, ist
// chanstate (Kanalabschnitte) und der Supervisor (Kopf).
package report

import (
	"encoding/json"
	"time"
)

// Version ist die Fassung des Datensatzformats.
const Version = 1

// Die Werte des Feldes trigger. Es sagt dem Server, warum dieser Datensatz kam.
const (
	TriggerInterval = "interval"
	TriggerStartup  = "startup"
	TriggerAlert    = "alert"
	TriggerShutdown = "shutdown"
)

// Die Werte des Feldes rx_state.
const (
	RxStarting = "starting"
	RxRunning  = "running"
	RxFailed   = "failed"
	RxStopped  = "stopped"
	// RxStalled steht für einen Kanal, dessen Zustandsmaschine nicht innerhalb
	// der Frist geantwortet hat. Sein Hängen ist selbst die Meldung.
	RxStalled = "stalled"
)

// Die Werte des Feldes time_source.
const (
	ZeitAusEnsemble = "ens"
	ZeitAusKnoten   = "node"
)

// Report ist ein vollständiger Datensatz.
type Report struct {
	ReportVersion int       `json:"report_version"`
	Seq           uint64    `json:"seq"`
	GeneratedAt   string    `json:"generated_at"`
	Window        Fenster   `json:"window"`
	Trigger       string    `json:"trigger"`
	Node          Node      `json:"node"`
	Channels      []Channel `json:"channels"`
	Counters      Counters  `json:"counters"`
}

// Fenster ist der Zeitraum, den der Datensatz abdeckt.
type Fenster struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Node ist der Kopf: wer meldet, von wo, seit wann.
type Node struct {
	NodeID       string   `json:"node_id"`
	Name         string   `json:"name"`
	PubKey       string   `json:"pubkey"`
	LocationCode string   `json:"location_code"`
	Location     Location `json:"location"`
	Antenna      string   `json:"antenna,omitempty"`
	Contact      string   `json:"contact,omitempty"`
	NodeVersion  string   `json:"node_version"`
	NodeCommit   string   `json:"node_commit"`
	Platform     string   `json:"platform"`
	StartedAt    string   `json:"started_at"`
	UptimeS      int64    `json:"uptime_s"`
	Clock        Clock    `json:"clock"`
	Spool        Spool    `json:"spool"`
}

// Location ist der dekodierte Knotenstandort. Das Rechteck ist rund 1 km groß —
// bewusst grob, das schützt die Freiwilligen.
type Location struct {
	Zone   int     `json:"zone"`
	Digits string  `json:"digits"`
	LatMin float64 `json:"lat_min"`
	LatMax float64 `json:"lat_max"`
	LonMin float64 `json:"lon_min"`
	LonMax float64 `json:"lon_max"`
	Lat    float64 `json:"lat"`
	Lon    float64 `json:"lon"`
}

// Clock ist die Uhrenauskunft des Knotens.
type Clock struct {
	NtpSynchronized bool  `json:"ntp_synchronized"`
	EnsOffsetMs     int64 `json:"ens_offset_ms"`
}

// Spool ist der Stand des Store-and-Forward-Speichers.
type Spool struct {
	Reports    int   `json:"reports"`
	Bytes      int64 `json:"bytes"`
	AudioFiles int   `json:"audio_files"`
}

// Counters sind die knotenweiten Zähler.
type Counters struct {
	Panics          uint64 `json:"panics"`
	UnknownRecords  uint64 `json:"unknown_records"`
	ReportsSpooled  uint64 `json:"reports_spooled"`
	ReportsDropped  uint64 `json:"reports_dropped"`
	ReportsRejected uint64 `json:"reports_rejected"`
}

// Channel ist der Abschnitt eines überwachten DAB-Kanals.
type Channel struct {
	Channel      string    `json:"channel"`
	FreqHz       int64     `json:"freq_hz"`
	Device       string    `json:"device"`
	DeviceSerial string    `json:"device_serial"`
	RxState      string    `json:"rx_state"`
	RxVersion    string    `json:"rx_version"`
	RxCommit     string    `json:"rx_commit"`
	WelleCommit  string    `json:"welle_commit"`
	RxRestarts   int       `json:"rx_restarts"`
	LastError    string    `json:"last_error"`
	Ensemble     *Ensemble `json:"ensemble"`
	Reception    Reception `json:"reception"`
	Asa          Asa       `json:"asa"`
}

// Ensemble ist der Multiplex-Aufbau, wie ihn asamon-rx meldet.
type Ensemble struct {
	EnsHash        string    `json:"ens_hash"`
	EnsContentHash string    `json:"ens_content_hash"`
	Eid            string    `json:"eid"`
	Ecc            int       `json:"ecc"`
	Label          string    `json:"label"`
	FirstSeen      string    `json:"first_seen"`
	LastSeen       string    `json:"last_seen"`
	Services       []Service `json:"services"`
}

// Service ist ein Dienst des Ensembles.
type Service struct {
	Sid        string      `json:"sid"`
	Label      string      `json:"label"`
	Components []Component `json:"components"`
}

// Component ist eine Dienstkomponente mit Subchannel-Eintrag.
type Component struct {
	SubChID    int    `json:"subch_id"`
	StartAddr  int    `json:"start_addr"`
	Size       int    `json:"size"`
	Protection string `json:"protection"`
	Bitrate    int    `json:"bitrate"`
}

// Reception ist die Empfangsqualität über das Fenster.
//
// crc_err_rate ist die wichtigste Zahl im ganzen Datensatz: Mit ihr lässt sich
// "Ensemble sendet keinen Heartbeat" von "wir empfangen schlecht" trennen.
type Reception struct {
	Samples        int      `json:"samples"`
	SnrAvg         *float64 `json:"snr_avg"`
	SnrMin         *float64 `json:"snr_min"`
	SnrMax         *float64 `json:"snr_max"`
	SyncRatio      float64  `json:"sync_ratio"`
	FibTotal       uint64   `json:"fib_total"`
	FibCrcErr      uint64   `json:"fib_crc_err"`
	CrcErrRate     float64  `json:"crc_err_rate"`
	Dropped        uint64   `json:"dropped"`
	NodeDropped    uint64   `json:"node_dropped"`
	ParseErrors    uint64   `json:"parse_errors"`
	SeqGaps        uint64   `json:"seq_gaps"`
	BrokenLines    uint64   `json:"broken_lines"`
	EnsTimeOffsetM *int64   `json:"ens_time_offset_ms"`
}

// Asa ist der ASA-Befund des Kanals.
type Asa struct {
	// EverSeen sagt, ob von diesem Ensemble je ein FIG 0/15 kam. Ein Ensemble,
	// von dem noch nie ein Heartbeat kam, ist ein anderer Befund als eines,
	// dessen Heartbeat aussetzt — beides muss unterscheidbar bleiben.
	EverSeen  bool        `json:"ever_seen"`
	Observed  bool        `json:"observed"`
	Heartbeat Heartbeat   `json:"heartbeat"`
	Records   []AsaRecord `json:"records"`
	Alerts    []Alert     `json:"alerts"`
	Anomalies []string    `json:"anomalies,omitempty"`
}

// Heartbeat ist das Aggregat über das Fenster. Es steht neben der vollständigen
// Liste, nicht an ihrer Stelle: Der Server kann damit rechnen, ohne die Liste
// auszuwerten.
type Heartbeat struct {
	Expected int `json:"expected"`
	Received int `json:"received"`
	// Suppressed sind Sekunden, in denen laut Norm kein Heartbeat gesendet
	// wird, weil Alerts signalisiert werden. Sie fehlen nicht — sie entfallen.
	Suppressed     int      `json:"suppressed"`
	MissingSeconds []string `json:"missing_seconds"`
	PdMismatch     int      `json:"pd_mismatch"`
}

// AsaRecord ist eine einzelne FIG-0/15-Instanz des Fensters.
//
// Die Liste ist bewusst nicht aggregiert: Jeder Eintrag trägt seinen eigenen
// Hash, nur so kann der Server einzeln deduplizieren. Und raw ist der Beleg,
// aus dem sich jede Deutung nachträglich zurückrechnen lässt — es gibt keine
// Referenzimplementierung von FIG 0/15, und unser Parser wird Fehler haben.
type AsaRecord struct {
	AsaHash      string `json:"asa_hash"`
	EnsSecond    string `json:"ens_second"`
	TimeSource   string `json:"time_source"`
	Ts           string `json:"ts"`
	Heartbeat    bool   `json:"heartbeat"`
	Cn           bool   `json:"cn"`
	Oe           bool   `json:"oe"`
	PdSecondHalf bool   `json:"pd_second_half"`
	Raw          string `json:"raw"`
}

// Alert ist ein verfolgter Vorfall.
//
// Ein Alert erscheint in jedem Datensatz, solange er läuft, und ein letztes Mal
// mit closed: true. Der Server sieht ihn also mehrfach; alert_uid hält ihn
// zusammen.
type Alert struct {
	AlertUID          string  `json:"alert_uid"`
	AlertUIDConfident bool    `json:"alert_uid_confident"`
	Oe                bool    `json:"oe"`
	ChannelEid        string  `json:"channel_eid"`
	WarningEid        string  `json:"warning_eid"`
	SubChID           *int    `json:"subch_id"`
	Iid               *int    `json:"iid"`
	Stage             string  `json:"stage"`
	Level             *int    `json:"level"`
	Test              bool    `json:"test"`
	Phase             string  `json:"phase"`
	EnteredAtPhase    string  `json:"entered_at_phase"`
	FirstSeenEns      string  `json:"first_seen_ens"`
	LastSeenEns       string  `json:"last_seen_ens"`
	Closed            bool    `json:"closed"`
	CloseReason       string  `json:"close_reason"`
	Incomplete        bool    `json:"incomplete"`
	Gap               bool    `json:"gap"`
	Instances         int     `json:"instances"`
	ExpectedInstances int     `json:"expected_instances"`
	Phases            []Phase `json:"phases"`
	Area              Area    `json:"area"`
	Audio             *Audio  `json:"audio"`
}

// Phase ist ein Abschnitt im Phasenverlauf eines Alerts.
type Phase struct {
	Phase string `json:"phase"`
	From  string `json:"from"`
	To    string `json:"to,omitempty"`
	// Sec ist der Sekundenzähler aus der Pre-Trigger-Phase. 63 ist Sonderwert:
	// Start bei Sekunde 0, 5 s Triggerdauer.
	Sec *int `json:"sec,omitempty"`
}

// Area ist das Warngebiet.
type Area struct {
	// WholeEnsemble ist dreiwertig. true heißt: es gab keine Location Codes —
	// der Alert gilt für das gesamte Versorgungsgebiet. Das ist kein fehlendes
	// Feld, sondern eine Aussage. null heißt dagegen, dass der Knoten nie eine
	// Instanz mit Status-Feld gesehen hat und über das Warngebiet nichts sagen
	// kann; das kommt beim Einstieg in der Sustain-Phase vor.
	WholeEnsemble *bool           `json:"whole_ensemble"`
	Codes         []AreaCode      `json:"codes"`
	GeoJSON       json.RawMessage `json:"geojson,omitempty"`
	// Raw bleibt immer dabei, auch wenn die Geometrie gelingt.
	Raw string `json:"raw"`
	// DecodeError nennt den Grund, wenn die Location Codes nicht zu deuten
	// waren. Der Alert wird trotzdem gemeldet.
	DecodeError string `json:"decode_error,omitempty"`
}

// AreaCode ist ein Location Code des Warngebiets.
type AreaCode struct {
	Zone         int    `json:"zone"`
	Digits       string `json:"digits"`
	Presentation string `json:"presentation"`
	Rect         *Rect  `json:"rect"`
}

// Rect ist ein sphärisches Rechteck in WGS84-Grad.
type Rect struct {
	LatMin float64 `json:"lat_min"`
	LatMax float64 `json:"lat_max"`
	LonMin float64 `json:"lon_min"`
	LonMax float64 `json:"lon_max"`
}

// Audio ist der Stand des Mitschnitts zu einem Alert.
type Audio struct {
	State     string `json:"state"` // none|recording|stored|uploaded|failed
	SubChID   int    `json:"subch_id"`
	Bytes     int64  `json:"bytes"`
	StartedAt string `json:"started_at"`
	Sha256    string `json:"sha256"`
	Truncated bool   `json:"truncated"`
	// DurationSEst ist aus der Bitrate der Komponente geschätzt, nicht gemessen.
	DurationSEst float64 `json:"duration_s_est"`
	Gaps         int     `json:"audio_gaps"`
	UploadedAt   string  `json:"uploaded_at,omitempty"`
}

// Die Werte des Feldes audio.state.
const (
	AudioKeins     = "none"
	AudioLaeuft    = "recording"
	AudioGespeicht = "stored"
	AudioHochgel   = "uploaded"
	AudioFehler    = "failed"
)

// Zeitpunkt schreibt eine Zeit mit Millisekunden, wie sie im Kopf des
// Datensatzes steht.
func Zeitpunkt(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

// Sekundenzeit schreibt eine Zeit ohne Bruchteile — so, wie sie auch in die
// Hashes eingeht.
func Sekundenzeit(t time.Time) string {
	return t.UTC().Truncate(time.Second).Format("2006-01-02T15:04:05Z")
}
