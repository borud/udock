package udock

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	// we use the clue/darkhttpd
	httpEchoImage    = "hashicorp/http-echo:latest"
	httpInternalPort = "5678"
)

func TestClient(t *testing.T) {
	// Client creation and deferring closing client
	session, err := Create()
	if errors.Is(err, ErrConnectingToDocker) {
		t.Skip("docker not available, if you want these tests to run please make sure docker is running")
	}
	require.NoError(t, err)
	require.NotNil(t, session)
	defer func() {
		require.NoError(t, session.Close())
	}()

	// Verify that we can identify that we don't have an image.
	err = session.VerifyHaveImage("some/madeup:image")
	require.ErrorIs(t, err, ErrImageNotPresent)

	// Uncomment this if you want to test pulling image. Per default we don't test this
	// because it pings an external service which may blacklist us if we pull images too
	// frequently.
	// require.NoError(t, RemoveImage(client, testingImage))

	// This will pull the image
	err = session.PullImage(httpEchoImage)
	require.NoError(t, err)

	// allocate a random free port number for the external port
	freePort, err := getFreePort()
	require.NoError(t, err)
	httpExternalport := fmt.Sprintf("%d", freePort)

	// create the container
	containerID, err := session.CreateContainer(
		httpEchoImage,
		fmt.Sprintf("test-%d", time.Now().UnixNano()),
		map[string]string{httpExternalport: httpInternalPort},
	)
	require.NoError(t, err)
	slog.Info("created container", "containerID", containerID)

	defer func() {
		require.NoError(t, session.RemoveContainer(containerID))
		slog.Info("removed container", "containerID", containerID)
	}()

	// start the container
	err = session.StartContainer(containerID)
	require.NoError(t, err)
	slog.Info("started container", "containerID", containerID)

	// perform a HTTP request to ensure container is up
	resp, err := http.Get("http://localhost:" + httpExternalport + "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	require.Equal(t, 200, resp.StatusCode)
	require.Equal(t, "hello-world\n", string(body))
}
