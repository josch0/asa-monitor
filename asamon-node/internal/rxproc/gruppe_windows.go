// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package rxproc

import (
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"
)

// Waisenkinder unter Windows — und was dagegen hilft.
//
// Unter Linux nimmt systemd die Kindprozesse mit: Die Unit hat eine eigene
// Control Group, und beim Beenden räumt `KillMode=control-group` alles darin
// ab. Stirbt asamon-node hart — SIGKILL, Stromausfall im laufenden Betrieb,
// ein Absturz —, bleibt kein asamon-rx zurück.
//
// Windows kennt nichts dergleichen. Ein Kindprozess überlebt seinen Erzeuger
// ohne Weiteres, und ein überlebender asamon-rx hält den RTL-SDR-Stick offen:
// Der neu gestartete Knoten findet dann kein Gerät mehr und ist so lange tot,
// bis jemand von Hand aufräumt. Auf einem Rechner, den ein Freiwilliger
// einmal einrichtet und nie wieder anfasst, ist das kein Randfall.
//
// Das Windows-Gegenstück zur Control Group ist ein **Job Object** mit
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE: Alle zugewiesenen Prozesse werden
// beendet, sobald das letzte Handle darauf schließt — und das tut das
// Betriebssystem, wenn der Prozess endet, gleich auf welchem Weg.
//
// Die drei nötigen Aufrufe stehen nicht in `syscall`, wohl aber in
// kernel32.dll. Sie hier von Hand zu binden ist derselbe Handel wie beim
// sd_notify unter Linux: rund achtzig Zeilen gegen eine Fremdabhängigkeit,
// die ihr halbes Ökosystem mitbrächte (TODO.md Abschnitt 4).

var (
	kernel32                   = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW       = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObjec = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObje = kernel32.NewProc("AssignProcessToJobObject")
)

// jobObjectExtendedLimitInformation ist die Informationsklasse 9.
const jobObjectExtendedLimitInformation = 9

// jobObjectLimitKillOnJobClose beendet alle Prozesse des Jobs, sobald das
// letzte Handle darauf geschlossen wird.
const jobObjectLimitKillOnJobClose = 0x00002000

// processSetQuota fehlt in syscall; die Zahl steht in winnt.h.
// AssignProcessToJobObject verlangt PROCESS_SET_QUOTA und PROCESS_TERMINATE.
const processSetQuota = 0x0100

type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type jobObjectBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type jobObjectExtendedLimitInformationT struct {
	BasicLimitInformation jobObjectBasicLimitInformation
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

// Gruppe fasst die Kindprozesse zusammen, damit sie den Knoten nicht überleben.
type Gruppe struct {
	job syscall.Handle
}

// NeueGruppe legt ein Job Object an, das seine Prozesse beim Schließen beendet.
//
// Schlägt das fehl, kommt ein nil-Zeiger zurück und der Knoten läuft ohne
// diese Absicherung weiter: Ein möglicher Waisenprozess ist ein schlechterer
// Zustand als heute, aber ein nicht startender Knoten wäre ein schlechterer
// als beide.
func NeueGruppe() (*Gruppe, error) {
	handle, _, err := procCreateJobObjectW.Call(0, 0)
	if handle == 0 {
		return nil, fmt.Errorf("CreateJobObject: %w", err)
	}
	job := syscall.Handle(handle)

	info := jobObjectExtendedLimitInformationT{
		BasicLimitInformation: jobObjectBasicLimitInformation{
			LimitFlags: jobObjectLimitKillOnJobClose,
		},
	}
	ok, _, err := procSetInformationJobObjec.Call(
		uintptr(job),
		uintptr(jobObjectExtendedLimitInformation),
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info),
	)
	if ok == 0 {
		syscall.CloseHandle(job)
		return nil, fmt.Errorf("SetInformationJobObject: %w", err)
	}
	return &Gruppe{job: job}, nil
}

// Aufnehmen weist einen gestarteten Prozess der Gruppe zu.
func (g *Gruppe) Aufnehmen(cmd *exec.Cmd) error {
	if g == nil || cmd.Process == nil {
		return nil
	}
	// os.Process gibt sein Handle nicht heraus, also wird ein eigenes geöffnet.
	// AssignProcessToJobObject verlangt PROCESS_SET_QUOTA und PROCESS_TERMINATE.
	h, err := syscall.OpenProcess(processSetQuota|syscall.PROCESS_TERMINATE,
		false, uint32(cmd.Process.Pid))
	if err != nil {
		return fmt.Errorf("OpenProcess(%d): %w", cmd.Process.Pid, err)
	}
	defer syscall.CloseHandle(h)

	ok, _, err := procAssignProcessToJobObje.Call(uintptr(g.job), uintptr(h))
	if ok == 0 {
		return fmt.Errorf("AssignProcessToJobObject(%d): %w", cmd.Process.Pid, err)
	}
	return nil
}

// Schliessen gibt das Job Object frei — und beendet damit alles darin.
func (g *Gruppe) Schliessen() error {
	if g == nil {
		return nil
	}
	return syscall.CloseHandle(g.job)
}
