# gracefulshutdown [![Build Status](https://travis-ci.org/Zemanta/gracefulshutdown.svg)](https://travis-ci.org/Zemanta/gracefulshutdown)

Providing shutdown callbacks for graceful app shutdown

## Motivation



## Installation

```
go get github.com/Zemanta/gracefulshutdown
```

## Documentation

[GracefulShutdown](http://godoc.org/github.com/Zemanta/gracefulshutdown)

ShutdownManagers:
- [PosixSignalManager](http://godoc.org/github.com/Zemanta/gracefulshutdown/shutdownmanagers/posixsignal)
- [AwsManager](http://godoc.org/github.com/Zemanta/gracefulshutdown/shutdownmanagers/awsmanager)


## Example - aws

Graceful shutdown will listen for SQS messages on "example-sqs-queue". If a termination message has current EC2 instance id, it will run all callbacks in separate go routines. While callbacks are running it will call aws api RecordLifecycleActionHeartbeatInput autoscaler every 15 minutes. When callbacks return, the application will call aws api CompleteLifecycleAction. The callback will delay only if shutdown was initiated by awsmanager. If the message does not have current instance id, it will forward the message to correct instance via http on port 7999.

```go
package main

import (
	"fmt"
	"time"

	"github.com/Zemanta/gracefulshutdown"
	"github.com/Zemanta/gracefulshutdown/shutdownmanagers/awsmanager"
	"github.com/Zemanta/gracefulshutdown/shutdownmanagers/posixsignal"
)

func main() {
	// initialize gracefulshutdown with ping time
	gs := gracefulshutdown.New()

	// add posix shutdown manager
	gs.AddShutdownManager(posixsignal.NewPosixSignalManager())

	// set error handler
	gs.SetErrorHandler(gracefulshutdown.ErrorFunc(func(err error) {
		fmt.Println("Error:", err)
	}))

	// add aws shutdown manager
	gs.AddShutdownManager(awsmanager.NewAwsManager(&awsmanager.AwsManagerConfig{
		SqsQueueName:      "example-sqs-queue",
		LifecycleHookName: "example-lifecycle-hook",
		Port:              7999,
	}))

	// add your tasks that implement ShutdownCallback
	gs.AddShutdownCallback(gracefulshutdown.ShutdownFunc(func(shutdownManager string) error {
		fmt.Println("Shutdown callback start")
		if shutdownManager == awsmanager.Name {
			time.Sleep(time.Hour)
		}
		fmt.Println("Shutdown callback finished")
		return nil
	}))

	// start shutdown managers
	if err := gs.Start(); err != nil {
		fmt.Println("Start:", err)
		return
	}

	// do other stuff
	time.Sleep(time.Hour * 2)
}
```


## Example - posix signals

Graceful shutdown will listen for posix SIGINT and SIGTERM signals. When they are received it will run all callbacks in separate go routines. When callbacks return, the application will exit with os.Exit(0)

```go
package main

import (
	"fmt"
	"time"

	"github.com/Zemanta/gracefulshutdown"
	"github.com/Zemanta/gracefulshutdown/shutdownmanagers/posixsignal"
)

func main() {
	// initialize gracefulshutdown
	gs := gracefulshutdown.New()

	// add posix shutdown manager
	gs.AddShutdownManager(posixsignal.NewPosixSignalManager())

	// add your tasks that implement ShutdownCallback
	gs.AddShutdownCallback(gracefulshutdown.ShutdownFunc(func(string) error {
		fmt.Println("Shutdown callback start")
		time.Sleep(time.Second)
		fmt.Println("Shutdown callback finished")
		return nil
	}))

	// start shutdown managers
	if err := gs.Start(); err != nil {
		fmt.Println("Start:", err)
		return
	}

	// do other stuff
	time.Sleep(time.Hour)
}
```

## Example - posix signals with error handler

The same as above, except now we set an ErrorHandler that prints the error returned from ShutdownCallback.

```go
package main

import (
	"fmt"
	"time"
	"errors"

	"github.com/Zemanta/gracefulshutdown"
	"github.com/Zemanta/gracefulshutdown/shutdownmanagers/posixsignal"
)

func main() {
	// initialize gracefulshutdown
	gs := gracefulshutdown.New()

	// add posix shutdown manager
	gs.AddShutdownManager(posixsignal.NewPosixSignalManager())

	// set error handler
	gs.SetErrorHandler(gracefulshutdown.ErrorFunc(func(err error) {
		fmt.Println("Error:", err)
	}))

	// add your tasks that implement ShutdownCallback
	gs.AddShutdownCallback(gracefulshutdown.ShutdownFunc(func(string) error {
		fmt.Println("Shutdown callback start")
		time.Sleep(time.Second)
		fmt.Println("Shutdown callback finished")
		return errors.New("my-error")
	}))

	// start shutdown managers
	if err := gs.Start(); err != nil {
		fmt.Println("Start:", err)
		return
	}

	// do other stuff
	time.Sleep(time.Hour)
}
```

