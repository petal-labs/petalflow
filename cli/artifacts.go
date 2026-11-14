package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/petal-labs/petalflow/artifact"
)

const artifactAPIResponseLimit int64 = 1 << 20

var artifactPathSegmentPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// NewArtifactsCmd creates operator commands for run-scoped artifact transfer.
func NewArtifactsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "artifacts", Short: "Upload and download run artifacts"}
	cmd.PersistentFlags().String("server", defaultArtifactServerURL(), "PetalFlow daemon URL")
	cmd.PersistentFlags().Duration("timeout", 5*time.Minute, "Transfer timeout")
	cmd.AddCommand(newArtifactUploadCmd(), newArtifactDownloadCmd())
	return cmd
}

func newArtifactUploadCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "upload <run-id> <file>", Short: "Upload a file into a run's artifact scope",
		Args: cobra.ExactArgs(2), RunE: runArtifactUpload,
	}
	cmd.Flags().String("content-type", "application/octet-stream", "Artifact media type")
	return cmd
}

func newArtifactDownloadCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "download <run-id> <artifact-id>", Short: "Download and verify a run artifact",
		Args: cobra.ExactArgs(2), RunE: runArtifactDownload,
	}
	cmd.Flags().StringP("output", "o", "", "Output file (required; existing files are not overwritten)")
	_ = cmd.MarkFlagRequired("output")
	return cmd
}

func runArtifactUpload(cmd *cobra.Command, args []string) error {
	contentType, _ := cmd.Flags().GetString("content-type")
	if _, _, err := mime.ParseMediaType(contentType); err != nil {
		return exitError(exitInputParse, "invalid content type: %v", err)
	}
	file, err := os.Open(args[1])
	if err != nil {
		return exitError(exitFileNotFound, "opening artifact: %v", err)
	}
	defer file.Close()
	request, client, err := newArtifactAPIRequest(cmd, http.MethodPost, []string{"api", "runs", args[0], "artifacts"}, file)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", contentType)
	response, err := client.Do(request)
	if err != nil {
		return exitError(exitRuntime, "uploading artifact: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return artifactAPIStatusError(response)
	}
	return writeArtifactUploadResponse(cmd.OutOrStdout(), response.Body)
}

func runArtifactDownload(cmd *cobra.Command, args []string) error {
	output, _ := cmd.Flags().GetString("output")
	request, client, err := newArtifactAPIRequest(cmd, http.MethodGet, []string{"api", "runs", args[0], "artifacts", args[1]}, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return exitError(exitRuntime, "downloading artifact: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return artifactAPIStatusError(response)
	}
	return writeVerifiedArtifact(output, response)
}

func newArtifactAPIRequest(cmd *cobra.Command, method string, path []string, body io.Reader) (*http.Request, *http.Client, error) {
	token := strings.TrimSpace(os.Getenv("PETALFLOW_API_TOKEN"))
	if token == "" {
		return nil, nil, errors.New("PETALFLOW_API_TOKEN must be set")
	}
	serverURL, _ := cmd.Flags().GetString("server")
	endpoint, err := artifactAPIURL(serverURL, path)
	if err != nil {
		return nil, nil, err
	}
	request, err := http.NewRequestWithContext(cmd.Context(), method, endpoint, body)
	if err != nil {
		return nil, nil, exitError(exitInputParse, "creating artifact request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	timeout, _ := cmd.Flags().GetDuration("timeout")
	client := &http.Client{
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return request, client, nil
}

func artifactAPIURL(serverURL string, path []string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(serverURL))
	if err != nil || parsed.User != nil || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", exitError(exitInputParse, "invalid artifact server URL")
	}
	for _, segment := range path {
		if len(segment) == 0 || len(segment) > 4096 || segment == "." || segment == ".." || !artifactPathSegmentPattern.MatchString(segment) {
			return "", exitError(exitInputParse, "invalid artifact API identifier")
		}
	}
	endpoint, err := url.JoinPath(parsed.String(), path...)
	if err != nil {
		return "", exitError(exitInputParse, "invalid artifact API path")
	}
	return endpoint, nil
}

func writeVerifiedArtifact(path string, response *http.Response) (err error) {
	expected := strings.ToLower(strings.TrimSpace(response.Header.Get("X-Artifact-SHA256")))
	if decoded, decodeErr := hex.DecodeString(expected); decodeErr != nil || len(decoded) != sha256.Size {
		return errors.New("artifact response is missing a valid SHA-256 digest")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("creating artifact output: %w", err)
	}
	keep := false
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			if err == nil {
				err = closeErr
			}
			keep = false
		}
		if !keep {
			_ = os.Remove(path)
		}
	}()
	hash := sha256.New()
	if _, err = io.Copy(io.MultiWriter(file, hash), response.Body); err != nil {
		return fmt.Errorf("writing artifact: %w", err)
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if actual != expected {
		return errors.New("artifact digest verification failed")
	}
	keep = true
	return nil
}

func artifactAPIStatusError(response *http.Response) error {
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(response.Body, artifactAPIResponseLimit)).Decode(&payload)
	if artifactPathSegmentPattern.MatchString(payload.Error.Code) {
		return exitError(exitRuntime, "artifact API returned %s: %s", response.Status, payload.Error.Code)
	}
	return exitError(exitRuntime, "artifact API returned %s", response.Status)
}

type artifactUploadOutput struct {
	ArtifactID  string `json:"artifact_id"`
	URI         string `json:"uri"`
	DownloadURL string `json:"download_url"`
	Digest      string `json:"digest"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
}

func writeArtifactUploadResponse(output io.Writer, body io.Reader) error {
	var response artifactUploadOutput
	if err := json.NewDecoder(io.LimitReader(body, artifactAPIResponseLimit)).Decode(&response); err != nil {
		return exitError(exitRuntime, "invalid artifact upload response")
	}
	reference, err := artifact.ParseReferenceID(response.ArtifactID)
	if err != nil || response.Digest != reference.Digest || response.Size != reference.Size || response.ContentType != reference.ContentType {
		return exitError(exitRuntime, "invalid artifact upload response metadata")
	}
	uriReference, err := artifact.ParseReferenceURI(response.URI)
	if err != nil || uriReference != reference || response.DownloadURL == "" {
		return exitError(exitRuntime, "invalid artifact upload response reference")
	}
	if err := json.NewEncoder(output).Encode(response); err != nil {
		return fmt.Errorf("writing artifact upload response: %w", err)
	}
	return nil
}

func defaultArtifactServerURL() string {
	if configured := strings.TrimSpace(os.Getenv("PETALFLOW_API_URL")); configured != "" {
		return configured
	}
	return "http://127.0.0.1:8080"
}
