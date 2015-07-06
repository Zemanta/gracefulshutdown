# gracefulshutdown

Providing shutdown callbacks for graceful app shutdown

## Installation

```
go get github.com/Zemanta/gracefulshutdown
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
	// initialize gracefulshutdown with ping time
	gs := gracefulshutdown.New(time.Hour)

	// add posix shutdown manager
	gs.AddShutdownManager(posixsignal.NewPosixSignalManager())

	// add your tasks that implement ShutdownCallback
	gs.AddShutdownCallback(gracefulshutdown.ShutdownFunc(func() error {
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

## Example - aws

Graceful shutdown will listen for SQS messages on "example-sqs-queue". When a termination message with current EC2 instance id is received it will run all callbacks in separate go routines. While callbacks are running it will call aws api RecordLifecycleActionHeartbeatInput autoscaler every 15 minutes. When callbacks return, the application will call aws api CompleteLifecycleAction.

```go
package main

import (
	"fmt"
	"time"

	"github.com/Zemanta/gracefulshutdown"
	"github.com/Zemanta/gracefulshutdown/shutdownmanagers/posixsignal"
	"github.com/Zemanta/gracefulshutdown/shutdownmanagers/awsmanager"
)

func main() {
	// initialize gracefulshutdown with ping time
	gs := gracefulshutdown.New(time.Minute * 15)

	// add posix shutdown manager
	gs.AddShutdownManager(posixsignal.NewPosixSignalManager())

	// add aws shutdown manager
	gs.AddShutdownManager(awsmanager.NewAwsManager(nil, "example-sqs-queue", "example-lifecycle-hook-name"))

	// add your tasks that implement ShutdownCallback
	gs.AddShutdownCallback(gracefulshutdown.ShutdownFunc(func() error {
		fmt.Println("Shutdown callback start")
		time.Sleep(time.Hour)
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
