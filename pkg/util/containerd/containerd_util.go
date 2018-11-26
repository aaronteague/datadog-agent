// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2018 Datadog, Inc.

// +build containerd

package containerd

import (
	"context"
	"time"
	"sync"

	"github.com/DataDog/datadog-agent/pkg/config"
	"github.com/DataDog/datadog-agent/pkg/util/log"
	"github.com/DataDog/datadog-agent/pkg/util/retry"
	"github.com/containerd/containerd"
)

var (
	globalContainerdUtil *ContainerdUtil
	once          sync.Once
)

// ContainerdItf is the interface implementing a subset of methods that leverage the containerd api.
type ContainerdItf interface {
	GetEvents() containerd.EventService
	EnsureServing(ctx context.Context) error
	GetNamespaces(ctx context.Context) ([]string, error)
	Containers(ctx context.Context) ([]containerd.Container, error)
	Metadata(ctx context.Context) (containerd.Version, error)
}

// ContainerdUtil is the util used to interact with the containerd api.
type ContainerdUtil struct {
	cl        *containerd.Client
	initRetry retry.Retrier
}

// GetContainerdUtil creates the containerd util containing the containerd client and implementing the ContainerdItf
// Errors are handled in the retrier.
func GetContainerdUtil() (ContainerdItf, error) {
	once.Do(func() {
		globalContainerdUtil = &ContainerdUtil{}
		// Initialize the client in the connect method
		globalContainerdUtil.initRetry.SetupRetrier(&retry.Config{
			Name:          "containerdutil",
			AttemptMethod: globalContainerdUtil.connect,
			Strategy:      retry.RetryCount,
			RetryCount:    10,
			RetryDelay:    30 * time.Second,
		})
	})
	if err := globalContainerdUtil.initRetry.TriggerRetry(); err != nil {
		log.Error("Containerd init error: %s", err.Error())
		return nil, err
	}
	return globalContainerdUtil, nil
}

// Metadata is used to collect the version and revision of the Containerd API
func (c *ContainerdUtil) Metadata(ctx context.Context) (containerd.Version, error) {
	return c.cl.Version(ctx)
}

// Close is used when done with a ContainerdUtil
func (c *ContainerdUtil) Close() error {
	if c.cl == nil {
		return log.Errorf("Containerd Client not initialized")
	}
	return c.cl.Close()
}

// connect is our retry strategy, it can be retriggered when the check is running if we lose connectivity.
func (c *ContainerdUtil) connect() error {
	var err error
	if c.cl != nil {
		err = c.cl.Reconnect()
		if err != nil {
			log.Errorf("Could not reconnect to the containerd daemon: %v", err)
			return c.cl.Close() // Attempt to close connections to avoid overloading the GRPC
		}
		return nil
	}
	// If we lose the connection, let's reset the state including the Dial options
	socketAddress := config.Datadog.GetString("cri_socket_path")
	c.cl, err = containerd.New(socketAddress) // TODO 	ClientOpt to use grpc timeout
	return err
}

// EnsureServing checks if the containerd daemon is healthy and tries to reconnect if need be.
func (c *ContainerdUtil) EnsureServing(ctx context.Context) error {
	if c.cl != nil {
		//  Check if the current client is healthy
		s, err := c.cl.IsServing(ctx)
		if s {
			return nil
		}
		log.Errorf("Current client is not responding: %v", err)
	}
	err := c.initRetry.TriggerRetry()
	if err != nil {
		log.Errorf("Can't connect to containerd, will retry later: %v", err)
		return err
	}
	return nil
}

// GetEvents interfaces with the containerd api to get the event service.
func (c *ContainerdUtil) GetEvents() containerd.EventService {
	// Boilderplate to retrieve events from the client
	return c.cl.EventService()
}

// GetNamespaces interfaces with the containerd api to get the list of available namespaces.
func (c *ContainerdUtil) GetNamespaces(ctx context.Context) ([]string, error) {
	return c.cl.NamespaceService().List(ctx)
}

// Containers interfaces with the containerd api to get the list of Containers.
func (c *ContainerdUtil) Containers(ctx context.Context) ([]containerd.Container, error) {
	return c.cl.Containers(ctx)
}
