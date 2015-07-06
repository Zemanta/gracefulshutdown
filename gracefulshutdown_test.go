package gracefulshutdown

import (
	"errors"
	"testing"
	"time"
)

type SMPingFunc func() error

func (f SMPingFunc) Ping() error {
	return f()
}

func (f SMPingFunc) ShutdownFinish() error {
	return nil
}

func (f SMPingFunc) Start(gs GSInterface) error {
	return nil
}

type SMFinishFunc func() error

func (f SMFinishFunc) Ping() error {
	return nil
}

func (f SMFinishFunc) ShutdownFinish() error {
	return f()
}

func (f SMFinishFunc) Start(gs GSInterface) error {
	return nil
}

type SMStartFunc func() error

func (f SMStartFunc) Ping() error {
	return nil
}

func (f SMStartFunc) ShutdownFinish() error {
	return nil
}

func (f SMStartFunc) Start(gs GSInterface) error {
	return f()
}

func TestCallbacksGetCalled(t *testing.T) {
	gs := New(time.Millisecond)

	c := make(chan int, 100)
	for i := 0; i < 15; i++ {
		gs.AddShutdownCallback(ShutdownFunc(func() error {
			c <- 1
			return nil
		}))
	}

	gs.StartShutdown(SMPingFunc(func() error {
		return nil
	}))

	if len(c) != 15 {
		t.Error("Expected 15 elements in channel, got ", len(c))
	}
}

func TestStartGetsCalled(t *testing.T) {
	gs := New(time.Hour)

	c := make(chan int, 100)
	for i := 0; i < 15; i++ {
		gs.AddShutdownManager(SMStartFunc(func() error {
			c <- 1
			return nil
		}))
	}

	gs.Start()

	if len(c) != 15 {
		t.Error("Expected 15 Start to be called, got ", len(c))
	}
}

func TestStartErrorGetsReturned(t *testing.T) {
	gs := New(time.Hour)

	gs.AddShutdownManager(SMStartFunc(func() error {
		return errors.New("my-error")
	}))

	err := gs.Start()
	if err == nil || err.Error() != "my-error" {
		t.Error("Shutdown did not return my-error, got ", err)
	}
}

func TestPingGetsCalled(t *testing.T) {
	c := make(chan int, 100)
	gs := New(2 * time.Millisecond)

	gs.AddShutdownCallback(ShutdownFunc(func() error {
		time.Sleep(5 * time.Millisecond)
		return nil
	}))

	gs.StartShutdown(SMPingFunc(func() error {
		c <- 1
		return nil
	}))

	time.Sleep(5 * time.Millisecond)

	if len(c) != 3 {
		t.Error("Expected 3 pings, got ", len(c))
	}
}

func TestShutdownFinishGetsCalled(t *testing.T) {
	c := make(chan int, 100)
	gs := New(2 * time.Millisecond)

	gs.AddShutdownCallback(ShutdownFunc(func() error {
		time.Sleep(5 * time.Millisecond)
		return nil
	}))

	gs.StartShutdown(SMFinishFunc(func() error {
		c <- 1
		return nil
	}))

	if len(c) != 1 {
		t.Error("Expected 1 ShutdownFinish, got ", len(c))
	}
}

func TestErrorHandlerFromPing(t *testing.T) {
	c := make(chan int, 100)
	gs := New(2 * time.Millisecond)

	gs.AddErrorHandler(ErrorFunc(func(err error) {
		if err.Error() == "my-error" {
			c <- 1
		}
	}))

	gs.AddShutdownCallback(ShutdownFunc(func() error {
		time.Sleep(5 * time.Millisecond)
		return nil
	}))

	gs.StartShutdown(SMPingFunc(func() error {
		return errors.New("my-error")
	}))

	time.Sleep(5 * time.Millisecond)

	if len(c) != 3 {
		t.Error("Expected 3 errors from pings, got ", len(c))
	}
}

func TestErrorHandlerFromFinishShutdown(t *testing.T) {
	c := make(chan int, 100)
	gs := New(2 * time.Millisecond)

	gs.AddErrorHandler(ErrorFunc(func(err error) {
		if err.Error() == "my-error" {
			c <- 1
		}
	}))

	gs.StartShutdown(SMFinishFunc(func() error {
		return errors.New("my-error")
	}))

	if len(c) != 1 {
		t.Error("Expected 1 error from ShutdownFinish, got ", len(c))
	}
}

func TestErrorHandlerFromCallbacks(t *testing.T) {
	c := make(chan int, 100)
	gs := New(2 * time.Millisecond)

	gs.AddErrorHandler(ErrorFunc(func(err error) {
		if err.Error() == "my-error" {
			c <- 1
		}
	}))

	for i := 0; i < 15; i++ {
		gs.AddShutdownCallback(ShutdownFunc(func() error {
			return errors.New("my-error")
		}))
	}

	gs.StartShutdown(SMFinishFunc(func() error {
		return nil
	}))

	if len(c) != 15 {
		t.Error("Expected 15 error from ShutdownCallbacks, got ", len(c))
	}
}

func TestErrorHandlerDirect(t *testing.T) {
	c := make(chan int, 100)
	gs := New(2 * time.Millisecond)

	gs.AddErrorHandler(ErrorFunc(func(err error) {
		if err.Error() == "my-error" {
			c <- 1
		}
	}))

	gs.ReportError(errors.New("my-error"))

	if len(c) != 1 {
		t.Error("Expected 1 error from ReportError call, got ", len(c))
	}
}
