// Package udock implements a simplified Docker API.
package udock

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

const (
	// dockerConnectTimeout is the timeout for connecting to docker and pinging docker.
	dockerConnectTimeout = 10 * time.Second

	// dockerImageVerifyTimeout is the timeout for verifying that we have a given docker image.
	dockerImageVerifyTimeout = 10 * time.Second

	// dockerPullTimeout is the timeout for pulling a docker image. Should be
	// high enough to account for the time it takes to download the image.
	dockerPullTimeout = 60 * time.Second

	// dockerCreateContainerTimeout is the timeout for creating container.
	dockerCreateContainerTimeout = 10 * time.Second

	// dockerStartContainerTimeout is the timeout for starting a container
	// including waiting for the container to start.
	dockerStartContainerTimeout = 10 * time.Second

	// stopContainerTimeout is the timeout for stopping a container.
	dockerRemoveContainerTimeout = 10 * time.Second

	// dockerRemoveImageTimeout is the timeout for removing image.
	dockerRemoveImageTimeout = 10 * time.Second
)

// package errors
var (
	ErrCreatingDockerClient = errors.New("error creating docker client")
	ErrConnectingToDocker   = errors.New("error connecting to docker")
	ErrListingImages        = errors.New("error listing docker images")
	ErrImageNotPresent      = errors.New("docker image is not present")
	ErrReadingPulledImage   = errors.New("error reading image during pull")
	ErrPullingImage         = errors.New("error pulling image")
	ErrCreatingContainer    = errors.New("error creating container")
	ErrStartingContainer    = errors.New("error starting container")
	ErrTimeout              = errors.New("operation timed out")
	ErrPortMap              = errors.New("portmap error")
)

// CreateClient creates a docker client.
func CreateClient() (*client.Client, error) {
	client, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, errors.Join(ErrCreatingDockerClient, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), dockerConnectTimeout)
	defer cancel()

	_, err = client.Ping(ctx)
	if err != nil {
		return nil, errors.Join(ErrConnectingToDocker, err)
	}

	return client, nil
}

// VerifyHaveImage returns a nil error if we have the image and an error if the
// docker image is missing or an error occurred when probing if we have the
// image.
func VerifyHaveImage(client *client.Client, dockerImage string) error {
	ctx, cancel := context.WithTimeout(context.Background(), dockerImageVerifyTimeout)
	defer cancel()

	filterArgs := filters.NewArgs()
	filterArgs.Add("reference", dockerImage)

	images, err := client.ImageList(ctx, image.ListOptions{Filters: filterArgs})
	if err != nil {
		return errors.Join(ErrListingImages, err)
	}

	// if the length is nonzero it means we have the image already.
	if len(images) > 0 {
		return nil
	}

	return fmt.Errorf("%w: %s", ErrImageNotPresent, dockerImage)
}

// PullImage pulls a docker image.  Returns a nil error if ok and an error value if something went wrong.
func PullImage(client *client.Client, dockerImage string) error {
	err := VerifyHaveImage(client, dockerImage)
	if err == nil {
		slog.Info("already have image, not pulling", "dockerImage", dockerImage)
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), dockerPullTimeout)
	defer cancel()

	slog.Info("did not have image, pulling", "dockerImage", dockerImage)
	image, err := client.ImagePull(ctx, dockerImage, image.PullOptions{All: false})
	if err != nil {
		return errors.Join(fmt.Errorf("%w: %s", ErrPullingImage, dockerImage), err)
	}

	_, err = io.Copy(io.Discard, image)
	if err != nil {
		return errors.Join(ErrReadingPulledImage, err)
	}
	slog.Info("done pulling image", "dockerImage", dockerImage)
	return nil
}

// CreateContainer creates a container.  If the operation succeeds we return a
// containerID and error is nil.  If an error occurs, the container ID is empty
// and the error is set.
func CreateContainer(client *client.Client, dockerImage string, containerName string, ports map[string]string) (string, error) {
	containerConfig := &container.Config{
		Image: dockerImage,
		Tty:   false,
	}

	portmap := nat.PortMap{}
	for hPort, cPort := range ports {
		containerPort, err := nat.NewPort("tcp", cPort)
		if err != nil {
			return "", errors.Join(ErrPortMap, err)
		}

		portmap[containerPort] = []nat.PortBinding{{
			HostIP:   "0.0.0.0",
			HostPort: hPort,
		}}
	}

	containerHostConfig := &container.HostConfig{
		PortBindings: portmap,
		AutoRemove:   true,
	}

	ctx, cancel := context.WithTimeout(context.Background(), dockerCreateContainerTimeout)
	defer cancel()

	container, err := client.ContainerCreate(
		ctx,
		containerConfig,
		containerHostConfig,
		nil, // network config
		nil, // platform
		containerName,
	)
	if err != nil {
		return "", errors.Join(ErrCreatingContainer, err)
	}

	return container.ID, nil
}

// StartContainer starts a docker container that has already been created.
func StartContainer(client *client.Client, containerID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), dockerStartContainerTimeout)
	defer cancel()

	// fire up the container
	err := client.ContainerStart(ctx, containerID, container.StartOptions{})
	if err != nil {
		return errors.Join(fmt.Errorf("%w: %s", ErrStartingContainer, containerID), err)
	}

	// wait for the container to start
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			state, err := client.ContainerInspect(ctx, containerID)
			if err != nil {
				return errors.Join(fmt.Errorf("%w: %s", ErrStartingContainer, containerID), err)
			}
			if state.State.Running {
				return nil
			}

		case <-ctx.Done():
			return ErrTimeout
		}
	}
}

// RemoveContainer removes a container and forces removal of volumes.  If the
// container is running it is shut down first.
func RemoveContainer(client *client.Client, containerID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), dockerRemoveContainerTimeout)
	defer cancel()

	return client.ContainerRemove(ctx, containerID, container.RemoveOptions{
		RemoveVolumes: true,
		Force:         true,
	})
}

// RemoveImage removes a docker image.
func RemoveImage(client *client.Client, dockerImage string) error {
	ctx, cancel := context.WithTimeout(context.Background(), dockerRemoveImageTimeout)
	defer cancel()

	_, err := client.ImageRemove(ctx, dockerImage, image.RemoveOptions{})
	return err
}

func getFreePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("failed to get a free port: %w", err)
	}
	defer listener.Close()

	// Extract the port from the listener address
	addr := listener.Addr().(*net.TCPAddr)
	return addr.Port, nil
}
