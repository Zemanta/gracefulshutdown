package gracefulshutdown

import (
	"sync"
	"time"
)

type ShutdownCallback interface {
	OnShutdown() error
}

type ShutdownFunc func() error

func (f ShutdownFunc) OnShutdown() error {
	return f()
}

type StartShutdownInterface interface {
	StartShutdown(sm ShutdownManager)
}

type ShutdownManager interface {
	Start(ssi StartShutdownInterface) error
	Ping()
	ShutdownFinish()
}

type GracefulShutdown struct {
	callbacks  []ShutdownCallback
	managers   []ShutdownManager

	pingTime time.Duration
}

func New(pingTime time.Duration) *GracefulShutdown {
	return &GracefulShutdown{
		callbacks:  make([]ShutdownCallback, 0, 10),
		managers:   make([]ShutdownManager, 0, 3),
		pingTime:   pingTime,
	}
}

func (gs *GracefulShutdown) Start() error {
	for _, manager := range gs.managers {
		if err := manager.Start(gs); err != nil {
			return err
		}
	}

	return nil
}

func (gs *GracefulShutdown) AddShutdownManager(manager ShutdownManager) {
	gs.managers = append(gs.managers, manager)
}

func (gs *GracefulShutdown) AddShutdownCallback(shutdownCallback ShutdownCallback) {
	gs.callbacks = append(gs.callbacks, shutdownCallback)
}

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
