package posixsignal

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/Zemanta/gracefulshutdown"
)

type PosixSignalManager struct {
	signals []os.Signal
}

func NewPosixSignalManager(sig ...os.Signal) *PosixSignalManager {
	if len(sig) == 0 {
		sig = make([]os.Signal, 2)
		sig[0] = os.Interrupt
		sig[1] = syscall.SIGTERM
	}
	return &PosixSignalManager{
		signals: sig,
	}
}

func (posixSignalManager *PosixSignalManager) Start(ssi gracefulshutdown.StartShutdownInterface) error {
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, posixSignalManager.signals...)

		// Block until a signal is received.
		<-c

		ssi.StartShutdown(posixSignalManager)
	}()

	return nil
}

func (posixSignalManager *PosixSignalManager) Ping() {

}

func (posixSignalManager *PosixSignalManager) ShutdownFinish() {
	os.Exit(0)
}
