package server

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/petal-labs/petalflow/artifact"
	"github.com/petal-labs/petalflow/runtime"
)

const runExportVersion = 1

// RunExportArtifact describes one digest-verifiable file in a run export.
type RunExportArtifact struct {
	ArtifactID  string `json:"artifact_id"`
	Path        string `json:"path"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
}

// RunExportManifest describes the immutable contents of a run export bundle.
type RunExportManifest struct {
	Version      int                 `json:"version"`
	RunID        string              `json:"run_id"`
	TenantID     string              `json:"tenant_id"`
	WorkflowID   string              `json:"workflow_id,omitempty"`
	ExportedAt   time.Time           `json:"exported_at"`
	RunSHA256    string              `json:"run_sha256"`
	EventsSHA256 string              `json:"events_sha256"`
	Artifacts    []RunExportArtifact `json:"artifacts"`
}

type preparedRunExport struct {
	record    *runtime.RunRecord
	events    []runtime.Event
	artifacts []artifact.Metadata
	manifest  RunExportManifest
}

func (s *Server) handleExportRun(w http.ResponseWriter, r *http.Request) {
	record, ok := s.authorizeArtifactRun(w, r)
	if !ok {
		return
	}
	if !exportableRunStatus(record.Status) {
		writeError(w, http.StatusConflict, "RUN_NOT_SETTLED", "run export requires a terminal run")
		return
	}
	if s.eventStore == nil {
		writeError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "event storage is not configured")
		return
	}
	prepared, err := s.prepareRunExport(r.Context(), record)
	if err != nil {
		s.logger.Error("preparing run export", "run_id", record.ID, "error", err)
		writeError(w, http.StatusServiceUnavailable, "RUN_EXPORT_ERROR", "run export unavailable")
		return
	}
	file, digest, err := s.buildRunExport(r.Context(), prepared)
	if err != nil {
		s.logger.Error("building run export", "run_id", record.ID, "error", err)
		writeError(w, http.StatusServiceUnavailable, "RUN_EXPORT_ERROR", "run export unavailable")
		return
	}
	defer func() {
		name := file.Name()
		if closeErr := file.Close(); closeErr != nil {
			s.logger.Warn("closing run export", "run_id", record.ID, "error", closeErr)
		}
		if removeErr := os.Remove(name); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			s.logger.Warn("removing run export", "run_id", record.ID, "error", removeErr)
		}
	}()
	info, err := file.Stat()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "RUN_EXPORT_ERROR", "run export unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": record.ID + ".tar.gz"}))
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("X-Export-SHA256", digest)
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, file); err != nil {
		s.logger.Warn("streaming run export", "run_id", record.ID, "error", err)
	}
}

func exportableRunStatus(status runtime.RunStatus) bool {
	return status == runtime.RunStatusCompleted || status == runtime.RunStatusFailed || status == runtime.RunStatusCanceled
}

func (s *Server) prepareRunExport(ctx context.Context, record *runtime.RunRecord) (preparedRunExport, error) {
	events, err := s.eventStore.List(ctx, record.ID, 0, 0)
	if err != nil {
		return preparedRunExport{}, fmt.Errorf("list events: %w", err)
	}
	items, err := s.artifactStore.ListByRun(ctx, artifact.Scope{TenantID: record.TenantID, RunID: record.ID})
	if err != nil {
		return preparedRunExport{}, fmt.Errorf("list artifacts: %w", err)
	}
	referenced := make([]artifact.Metadata, 0, len(items))
	for _, item := range items {
		if containsArtifactReference(record, item.Reference) || containsArtifactReference(events, item.Reference) {
			referenced = append(referenced, item)
		}
	}
	safeRecord := *record
	safeRecord.ResumeToken = ""
	runJSON, err := marshalExportJSON(&safeRecord)
	if err != nil {
		return preparedRunExport{}, err
	}
	eventsJSON, err := marshalExportJSON(events)
	if err != nil {
		return preparedRunExport{}, err
	}
	manifest := RunExportManifest{
		Version: runExportVersion, RunID: record.ID, TenantID: record.TenantID,
		WorkflowID: record.WorkflowID, ExportedAt: time.Now().UTC(),
		RunSHA256: sha256Hex(runJSON), EventsSHA256: sha256Hex(eventsJSON),
		Artifacts: make([]RunExportArtifact, 0, len(referenced)),
	}
	for _, item := range referenced {
		id := artifact.ReferenceID(item.Reference)
		manifest.Artifacts = append(manifest.Artifacts, RunExportArtifact{
			ArtifactID: id, Path: "artifacts/" + id, SHA256: item.Reference.Digest,
			Size: item.Reference.Size, ContentType: item.Reference.ContentType,
		})
	}
	return preparedRunExport{record: &safeRecord, events: events, artifacts: referenced, manifest: manifest}, nil
}

func (s *Server) buildRunExport(ctx context.Context, prepared preparedRunExport) (*os.File, string, error) {
	file, err := os.CreateTemp("", "petalflow-run-export-*.tar.gz")
	if err != nil {
		return nil, "", fmt.Errorf("create export: %w", err)
	}
	cleanup := func(buildErr error) (*os.File, string, error) {
		name := file.Name()
		_ = file.Close()
		_ = os.Remove(name)
		return nil, "", buildErr
	}
	hasher := sha256.New()
	gzipWriter := gzip.NewWriter(io.MultiWriter(file, hasher))
	tarWriter := tar.NewWriter(gzipWriter)
	if err := s.writeRunExportEntries(ctx, tarWriter, prepared); err != nil {
		return cleanup(err)
	}
	if err := tarWriter.Close(); err != nil {
		return cleanup(fmt.Errorf("close export tar: %w", err))
	}
	if err := gzipWriter.Close(); err != nil {
		return cleanup(fmt.Errorf("close export gzip: %w", err))
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return cleanup(fmt.Errorf("rewind export: %w", err))
	}
	return file, hex.EncodeToString(hasher.Sum(nil)), nil
}

func (s *Server) writeRunExportEntries(ctx context.Context, writer *tar.Writer, prepared preparedRunExport) error {
	manifestJSON, err := marshalExportJSON(prepared.manifest)
	if err != nil {
		return err
	}
	runJSON, err := marshalExportJSON(prepared.record)
	if err != nil {
		return err
	}
	eventsJSON, err := marshalExportJSON(prepared.events)
	if err != nil {
		return err
	}
	for _, entry := range []struct {
		name string
		data []byte
	}{{"manifest.json", manifestJSON}, {"run.json", runJSON}, {"events.json", eventsJSON}} {
		if err := writeTarBytes(writer, entry.name, entry.data); err != nil {
			return err
		}
	}
	for index, metadata := range prepared.artifacts {
		if err := s.writeTarArtifact(ctx, writer, prepared.manifest.Artifacts[index].Path, metadata); err != nil {
			return err
		}
	}
	return nil
}

func writeTarBytes(writer *tar.Writer, name string, data []byte) error {
	header := &tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), ModTime: time.Unix(0, 0).UTC()}
	if err := writer.WriteHeader(header); err != nil {
		return fmt.Errorf("write export header %s: %w", name, err)
	}
	if _, err := writer.Write(data); err != nil {
		return fmt.Errorf("write export entry %s: %w", name, err)
	}
	return nil
}

func (s *Server) writeTarArtifact(ctx context.Context, writer *tar.Writer, name string, metadata artifact.Metadata) error {
	reader, err := s.artifactStore.Get(ctx, metadata.Scope, metadata.Reference)
	if err != nil {
		return fmt.Errorf("open export artifact: %w", err)
	}
	defer reader.Close()
	header := &tar.Header{Name: name, Mode: 0o600, Size: metadata.Reference.Size, ModTime: time.Unix(0, 0).UTC()}
	if err := writer.WriteHeader(header); err != nil {
		return fmt.Errorf("write artifact header: %w", err)
	}
	hasher := sha256.New()
	written, err := io.CopyN(io.MultiWriter(writer, hasher), reader, metadata.Reference.Size)
	if err != nil || written != metadata.Reference.Size {
		return fmt.Errorf("copy export artifact: wrote %d: %w", written, err)
	}
	extra, readErr := io.CopyN(io.Discard, reader, 1)
	if extra != 0 || (readErr != nil && !errors.Is(readErr, io.EOF)) {
		return errors.New("export artifact size mismatch")
	}
	if hex.EncodeToString(hasher.Sum(nil)) != metadata.Reference.Digest {
		return errors.New("export artifact digest mismatch")
	}
	return nil
}

func marshalExportJSON(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal export JSON: %w", err)
	}
	return append(data, '\n'), nil
}

func sha256Hex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
