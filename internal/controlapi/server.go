package controlapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const maxRequestBodyBytes = 24 << 20

type errorResponse struct {
	APIVersion string `json:"api_version"`
	Error      string `json:"error"`
}

func NewHandler(manager *Manager) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", func(writer http.ResponseWriter, request *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]any{"api_version": APIVersion, "ok": true})
	})
	mux.HandleFunc("GET /v1/openapi.json", func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write(openAPIDocument)
	})
	mux.HandleFunc("GET /v1/capabilities", func(writer http.ResponseWriter, request *http.Request) {
		snapshot := manager.Snapshot()
		writeJSON(writer, http.StatusOK, map[string]any{
			"api_version":  snapshot.APIVersion,
			"connected":    snapshot.Connected,
			"capabilities": snapshot.Capabilities,
			"device":       snapshot.Device,
		})
	})
	mux.HandleFunc("GET /v1/state", func(writer http.ResponseWriter, request *http.Request) {
		writeJSON(writer, http.StatusOK, manager.Snapshot())
	})
	mux.HandleFunc("POST /v1/commands", func(writer http.ResponseWriter, request *http.Request) {
		handleCommands(writer, request, manager)
	})
	mux.HandleFunc("GET /v1/events", func(writer http.ResponseWriter, request *http.Request) {
		handleEvents(writer, request, manager)
	})
	return mux
}

func handleCommands(writer http.ResponseWriter, request *http.Request, manager *Manager) {
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBodyBytes)
	defer request.Body.Close()
	batch, operations, err := decodeCommandBatch(json.NewDecoder(request.Body), manager.Model())
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeError(writer, http.StatusRequestEntityTooLarge, "request body exceeds limit")
			return
		}
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}

	ack, err := manager.Submit(request.Context(), batch.RequestID, operations)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		writeError(writer, http.StatusServiceUnavailable, fmt.Sprintf("submit commands: %v", err))
		return
	}
	status := http.StatusOK
	if ack.Status == "queued" {
		status = http.StatusAccepted
	}
	writeJSON(writer, status, ack)
}

func handleEvents(writer http.ResponseWriter, request *http.Request, manager *Manager) {
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeError(writer, http.StatusInternalServerError, "streaming is not supported by this HTTP server")
		return
	}

	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	writer.Header().Set("X-Accel-Buffering", "no")
	events, unsubscribe := manager.Subscribe()
	defer unsubscribe()

	ready, err := json.Marshal(manager.Snapshot())
	if err != nil {
		return
	}
	if _, err := fmt.Fprintf(writer, "event: ready\ndata: %s\n\n", ready); err != nil {
		return
	}
	flusher.Flush()

	keepAlive := time.NewTicker(15 * time.Second)
	defer keepAlive.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case event, open := <-events:
			if !open {
				return
			}
			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(writer, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Kind, data); err != nil {
				return
			}
			flusher.Flush()
		case <-keepAlive.C:
			if _, err := io.WriteString(writer, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeError(writer http.ResponseWriter, status int, message string) {
	writeJSON(writer, status, errorResponse{APIVersion: APIVersion, Error: message})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
