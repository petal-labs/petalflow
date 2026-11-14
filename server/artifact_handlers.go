package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/petal-labs/petalflow/artifact"
	"github.com/petal-labs/petalflow/runtime"
)

const artifactMultipartOverhead int64 = 1 << 20

var errArtifactUploadTooLarge = errors.New("artifact upload exceeds size limit")

// ArtifactUploadResponse identifies content accepted into a run's artifact scope.
type ArtifactUploadResponse struct {
	ArtifactID  string `json:"artifact_id"`
	URI         string `json:"uri"`
	DownloadURL string `json:"download_url"`
	Digest      string `json:"digest"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
}

func (s *Server) handleUploadArtifact(w http.ResponseWriter, r *http.Request) {
	record, ok := s.authorizeArtifactRun(w, r)
	if !ok {
		return
	}
	reader, contentType, closePart, err := artifactUploadReader(r)
	if closePart != nil {
		defer func() {
			if closeErr := closePart(); closeErr != nil {
				s.logger.Warn("closing artifact upload part", "run_id", record.ID, "error", closeErr)
			}
		}()
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARTIFACT_UPLOAD", "invalid artifact upload")
		return
	}
	if err := s.validateArtifactContentType(contentType); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "ARTIFACT_POLICY_REJECTED", err.Error())
		return
	}
	request := artifact.PutRequest{
		Scope:       artifact.Scope{TenantID: record.TenantID, RunID: record.ID},
		WorkflowID:  record.WorkflowID,
		ContentType: contentType,
	}
	limited := &artifactUploadLimitReader{reader: reader, max: s.artifactPolicy.MaxArtifactBytes}
	hasher := sha256.New()
	metadata, err := s.artifactStore.Put(r.Context(), request, io.TeeReader(limited, hasher))
	if err != nil {
		s.writeArtifactStoreError(w, err)
		return
	}
	if err := validateUploadedArtifact(metadata, request.Scope, limited.read, hex.EncodeToString(hasher.Sum(nil))); err != nil {
		if deleteErr := s.artifactStore.Delete(r.Context(), request.Scope, metadata.Reference); deleteErr != nil {
			s.logger.Warn("rolling back invalid artifact upload", "run_id", record.ID, "error", deleteErr)
		}
		writeError(w, http.StatusServiceUnavailable, "ARTIFACT_STORE_ERROR", "artifact store unavailable")
		return
	}
	id := artifact.ReferenceID(metadata.Reference)
	writeJSON(w, http.StatusCreated, ArtifactUploadResponse{
		ArtifactID: id, URI: artifact.ReferenceURI(metadata.Reference),
		DownloadURL: "/api/runs/" + record.ID + "/artifacts/" + id,
		Digest:      metadata.Reference.Digest, Size: metadata.Reference.Size,
		ContentType: metadata.Reference.ContentType,
	})
}

func (s *Server) handleDownloadArtifact(w http.ResponseWriter, r *http.Request) {
	record, ok := s.authorizeArtifactRun(w, r)
	if !ok {
		return
	}
	reference, err := artifact.ParseReferenceID(r.PathValue("artifact_id"))
	if err != nil {
		writeHiddenResource(w)
		return
	}
	scope := artifact.Scope{TenantID: record.TenantID, RunID: record.ID}
	metadata, err := s.artifactStore.Stat(r.Context(), scope, reference)
	if err != nil {
		s.writeArtifactLookupError(w, err)
		return
	}
	if metadata.Scope != scope || metadata.Reference != reference {
		writeError(w, http.StatusServiceUnavailable, "ARTIFACT_STORE_ERROR", "artifact store unavailable")
		return
	}
	reader, err := s.artifactStore.Get(r.Context(), scope, metadata.Reference)
	if err != nil {
		s.writeArtifactLookupError(w, err)
		return
	}
	defer func() {
		if closeErr := reader.Close(); closeErr != nil {
			s.logger.Warn("closing artifact download", "run_id", record.ID, "error", closeErr)
		}
	}()
	writeArtifactDownloadHeaders(w, metadata.Reference)
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, reader); err != nil {
		s.logger.Warn("artifact download interrupted", "run_id", record.ID, "error", err)
	}
}

func validateUploadedArtifact(metadata artifact.Metadata, scope artifact.Scope, size int64, digest string) error {
	if err := metadata.Reference.Validate(); err != nil {
		return err
	}
	if metadata.Scope != scope || metadata.Reference.Size != size || metadata.Reference.Digest != digest {
		return errors.New("artifact store returned mismatched upload metadata")
	}
	return nil
}

func (s *Server) authorizeArtifactRun(w http.ResponseWriter, r *http.Request) (*runtime.RunRecord, bool) {
	if s.artifactStore == nil || s.runStore == nil {
		writeError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "artifact storage is not configured")
		return nil, false
	}
	record, err := s.getOwnedRun(r)
	if err != nil {
		if errors.Is(err, runtime.ErrRunNotFound) {
			writeHiddenResource(w)
		} else {
			s.writeRunStoreError(w, err)
		}
		return nil, false
	}
	return record, true
}

func artifactUploadReader(r *http.Request) (io.Reader, string, func() error, error) {
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.Contains(mediaType, "/") {
		return nil, "", nil, errors.New("invalid content type")
	}
	if mediaType != "multipart/form-data" {
		return r.Body, mime.FormatMediaType(strings.ToLower(mediaType), params), nil, nil
	}
	boundary := params["boundary"]
	if boundary == "" {
		return nil, "", nil, errors.New("multipart boundary is required")
	}
	part, err := multipart.NewReader(r.Body, boundary).NextPart()
	if err != nil || part.FormName() != "file" || part.FileName() == "" {
		return nil, "", nil, errors.New("multipart file part is required")
	}
	partType, _, err := mime.ParseMediaType(part.Header.Get("Content-Type"))
	if err != nil || !strings.Contains(partType, "/") {
		_ = part.Close()
		return nil, "", nil, errors.New("multipart file content type is required")
	}
	return part, strings.ToLower(partType), part.Close, nil
}

func (s *Server) validateArtifactContentType(value string) error {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return errors.New("invalid artifact content type")
	}
	if len(s.artifactPolicy.AllowedContentTypes) == 0 {
		return nil
	}
	for _, allowed := range s.artifactPolicy.AllowedContentTypes {
		allowedType, _, parseErr := mime.ParseMediaType(allowed)
		if parseErr == nil && strings.EqualFold(mediaType, allowedType) {
			return nil
		}
	}
	return fmt.Errorf("artifact content type %q is not allowed", mediaType)
}

func (s *Server) writeArtifactStoreError(w http.ResponseWriter, err error) {
	var maxErr *http.MaxBytesError
	switch {
	case errors.Is(err, errArtifactUploadTooLarge), errors.As(err, &maxErr):
		writeError(w, http.StatusRequestEntityTooLarge, "ARTIFACT_TOO_LARGE", "artifact exceeds configured size limit")
	case errors.Is(err, artifact.ErrPolicyRejected):
		message := "artifact rejected by storage policy"
		if strings.Contains(strings.ToLower(err.Error()), "exceed") {
			writeError(w, http.StatusRequestEntityTooLarge, "ARTIFACT_TOO_LARGE", "artifact exceeds configured size limit")
			return
		}
		writeError(w, http.StatusUnprocessableEntity, "ARTIFACT_POLICY_REJECTED", message)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusRequestTimeout, "UPLOAD_CANCELED", "artifact upload was canceled")
	default:
		writeError(w, http.StatusServiceUnavailable, "ARTIFACT_STORE_ERROR", "artifact store unavailable")
	}
}

func (s *Server) writeArtifactLookupError(w http.ResponseWriter, err error) {
	if errors.Is(err, artifact.ErrNotFound) {
		writeHiddenResource(w)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "ARTIFACT_STORE_ERROR", "artifact store unavailable")
}

func writeArtifactDownloadHeaders(w http.ResponseWriter, reference artifact.Reference) {
	w.Header().Set("Content-Type", reference.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(reference.Size, 10))
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": reference.Digest}))
	digestBytes, _ := hex.DecodeString(reference.Digest)
	w.Header().Set("Digest", "sha-256="+base64.StdEncoding.EncodeToString(digestBytes))
	w.Header().Set("X-Artifact-SHA256", reference.Digest)
	w.Header().Set("ETag", `"sha256-`+reference.Digest+`"`)
	w.Header().Set("Cache-Control", "private, no-store")
}

type artifactUploadLimitReader struct {
	reader io.Reader
	max    int64
	read   int64
}

func (r *artifactUploadLimitReader) Read(buffer []byte) (int, error) {
	if r.max <= 0 {
		return r.reader.Read(buffer)
	}
	remaining := r.max - r.read
	if remaining <= 0 {
		var probe [1]byte
		if count, err := r.reader.Read(probe[:]); count > 0 {
			return 0, errArtifactUploadTooLarge
		} else {
			return 0, err
		}
	}
	if int64(len(buffer)) > remaining {
		buffer = buffer[:remaining]
	}
	count, err := r.reader.Read(buffer)
	r.read += int64(count)
	return count, err
}
