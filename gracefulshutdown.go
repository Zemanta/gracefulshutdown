/*

Providing shutdown callbacks for graceful app shutdown

Installation

To install run:

	go get github.com/Zemanta/gracefulshutdown
*/
package gracefulshutdown

import (
	"sync"
	"time"
)

// ShutdownCallback is an interface you have to implement for callbacks.
// OnShutdown will be called when shutdown is requested.
type ShutdownCallback interface {
	OnShutdown() error
}

// ShutdownFunc is a helper type, so you can easily provide anonymous functions
// as ShutdownCallbacks.
type ShutdownFunc func() error

func (f ShutdownFunc) OnShutdown() error {
	return f()
}

// StartShutdownInterface is an interface implemented by GracefulShutdown,
// that gets passed to ShutdownManager to call StartShutdown when shutdown
// is requested.
type StartShutdownInterface interface {
	StartShutdown(sm ShutdownManager)
}

// ShutdownManager is an interface implemnted by ShutdownManagers.
// ShutdownManagers start listening for shtudown requests in Start.
// When they call StartShutdown on StartShutdownInterface, Ping gets 
// called periodically, and once all ShutdownCallbacks return,
// ShutdownFinish is called.
type ShutdownManager interface {
	Start(ssi StartShutdownInterface) error
	Ping()
	ShutdownFinish()
}

// GracefulShutdown is main struct that handles ShutdownCallbacks and
// ShutdownManagers. Initialize it with New.
type GracefulShutdown struct {
	callbacks []ShutdownCallback
	managers  []ShutdownManager

	pingTime time.Duration
}

// New initializes GracefulShutdown. pingTime is the interval for calling
// pings on ShutdownManager when shutdown has started.
func New(pingTime time.Duration) *GracefulShutdown {
	return &GracefulShutdown{
		callbacks: make([]ShutdownCallback, 0, 10),
		managers:  make([]ShutdownManager, 0, 3),
		pingTime:  pingTime,
	}
}

// Start calls Start on all added ShutdownManagers. The ShutdownManagers
// start to listen to shutdown requests. Returns an error if any ShutdownManagers
// return an error.
func (gs *GracefulShutdown) Start() error {
	for _, manager := range gs.managers {
		if err := manager.Start(gs); err != nil {
			return err
		}
	}

	return nil
}

// AddShutdownManager adds a ShutdownManager that will listen to shutdown requests.
func (gs *GracefulShutdown) AddShutdownManager(manager ShutdownManager) {
	gs.managers = append(gs.managers, manager)
}

// AddShutdownCallback adds a ShutdownCallback that will be called when
// shutdown is requested.
//
// You can provide anything that implements ShutdownCallback interface,
// or you can supply a function like this:
//	AddShutdownCallback(gracefulshutdown.ShutdownFunc(func() error {
//		// callback code
//		return nil
//	}))
func (gs *GracefulShutdown) AddShutdownCallback(shutdownCallback ShutdownCallback) {
	gs.callbacks = append(gs.callbacks, shutdownCallback)
}

// StartShutdown is called from a ShutdownManager and will initiate shutdown:
// start sending pings, call all ShutdownCallbacks, wait for callbacks
// to finish and call ShutdownFinish on ShutdownManager
func (gs *GracefulShutdown) StartShutdown(sm ShutdownManager) {
	ticker := time.NewTicker(gs.pingTime)
	go func() {
		sm.Ping()
		for _ = range ticker.C {
			sm.Ping()
		}
	}()

	var wg sync.WaitGroup
	for _, shutdownCallback := range gs.callbacks {
		wg.Add(1)
		go func(shutdownCallback ShutdownCallback) {
			defer wg.Done()

			shutdownCallback.OnShutdown()
		}(shutdownCallback)
	}

	wg.Wait()

	ticker.Stop()

	sm.ShutdownFinish()
}
