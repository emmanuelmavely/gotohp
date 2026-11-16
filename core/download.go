package core

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"app/generated"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

// PrepareDownloadEndpoint is the RPC returning signed download URLs for one
// media item.
const PrepareDownloadEndpoint = "https://photosdata-pa.googleapis.com/$rpc/social.frontend.photos.preparedownloaddata.v1.PhotosPrepareDownloadDataService/PhotosPrepareDownload"

// DownloadURLs holds the signed download URLs for one media item. Both URLs
// expire quickly; use them right away.
type DownloadURLs struct {
	// OriginalURL points at the file as originally uploaded, without edits.
	OriginalURL string
	// EditedURL points at the file with applied edits, if any; it is empty
	// for unedited items.
	EditedURL string
}

// prepareDownloadRequest builds the PhotosPrepareDownload request for one
// media key.
func prepareDownloadRequest(mediaKey string) *generated.PrepareDownload {
	return &generated.PrepareDownload{
		Field1: &generated.PrepareDownloadField1Type{
			Field1: &generated.PrepareDownloadField1TypeField1Type{
				MediaKey: mediaKey,
			},
		},
		Field2: &generated.PrepareDownloadField2Type{
			Field1: &generated.PrepareDownloadField2TypeField1Type{
				Field7: &generated.PrepareDownloadField2TypeField1TypeField7Type{
					Field2: &generated.PrepareDownloadField2TypeField1TypeField7TypeField2Type{},
				},
			},
			Field5: &generated.PrepareDownloadField2TypeField5Type{
				Field2: &generated.PrepareDownloadField2TypeField5TypeField2Type{},
				Field3: &generated.PrepareDownloadField2TypeField5TypeField3Type{},
				// Field3 is explicitly present (zero) in the request the
				// mobile app sends.
				Field5: &generated.PrepareDownloadField2TypeField5TypeField5Type{
					Field1: &generated.PrepareDownloadField2TypeField5TypeField5TypeField1Type{},
					Field3: proto.Int64(0),
				},
			},
		},
	}
}

// GetDownloadURLs fetches the signed download URLs for one media key.
func (a *Api) GetDownloadURLs(mediaKey string) (*DownloadURLs, error) {
	serializedData, err := proto.Marshal(prepareDownloadRequest(mediaKey))
	if err != nil {
		return nil, fmt.Errorf("failed to marshal protobuf: %w", err)
	}

	bearerToken, err := a.BearerToken()
	if err != nil {
		return nil, fmt.Errorf("failed to get bearer token: %w", err)
	}

	req, err := http.NewRequest("POST", PrepareDownloadEndpoint, bytes.NewReader(serializedData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("User-Agent", a.userAgent)
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	req.Header.Set("x-goog-ext-173412678-bin", "CgcIAhClARgC")
	req.Header.Set("x-goog-ext-174067345-bin", "CgIIAg==")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := ReadResponseBody(resp)
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	bodyBytes, err := ReadResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	urls, fields, err := downloadURLsFromResponse(bodyBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to read prepare download response: %w", err)
	}
	if urls.OriginalURL == "" && urls.EditedURL == "" {
		// Report where the response did carry text, so a new layout can be
		// added; the values themselves are signed URLs and are left out.
		return nil, fmt.Errorf("prepare download returned no download URL for %s (text fields in the response: %s)",
			mediaKey, describeFieldPaths(fields))
	}
	return &urls, nil
}

// downloadURLsFromResponse finds the download URLs in a PhotosPrepareDownload
// response. The layout is only partly known: photos carry the edited file's
// URL at 1.5.2.5 and the original's at 1.5.2.6. Videos use a different
// container under 1.5, the way library items keep photo data at 5.2 and
// video data at 5.3, so every 1.5.N container is searched, 1.5.2 first. When
// no known slot holds a URL, the first URL anywhere in the response is used
// as the original; callers that need the exact original (the Live Photo
// repair) verify the downloaded file's SHA-1 anyway.
//
// It also returns the paths of all text fields, for error messages.
func downloadURLsFromResponse(body []byte) (DownloadURLs, []string, error) {
	found := map[string]string{}
	var textPaths []string
	if !collectProtoText(body, "", 0, found, &textPaths) {
		return DownloadURLs{}, nil, fmt.Errorf("malformed protobuf response")
	}

	paths := make([]string, 0, len(found))
	for path := range found {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	// Candidate containers: 1.5.2 first, then the other 1.5.N in order.
	containers := []string{"1.5.2"}
	for _, path := range paths {
		segments := strings.Split(path, ".")
		if len(segments) == 4 && segments[0] == "1" && segments[1] == "5" {
			if container := strings.Join(segments[:3], "."); !slices.Contains(containers, container) {
				containers = append(containers, container)
			}
		}
	}
	for _, container := range containers {
		urls := DownloadURLs{OriginalURL: found[container+".6"], EditedURL: found[container+".5"]}
		if urls.OriginalURL != "" || urls.EditedURL != "" {
			return urls, textPaths, nil
		}
	}
	if len(paths) > 0 {
		return DownloadURLs{OriginalURL: found[paths[0]]}, textPaths, nil
	}
	return DownloadURLs{}, textPaths, nil
}

// maxProtoDepth bounds the recursion into nested messages.
const maxProtoDepth = 10

// collectProtoText walks a protobuf message without a schema. Every
// length-delimited field that reads as printable text is recorded in
// textPaths, URLs also in urls (first value per path); other
// length-delimited fields are walked as nested messages. It returns false
// when message is not valid protobuf.
func collectProtoText(message []byte, prefix string, depth int, urls map[string]string, textPaths *[]string) bool {
	for len(message) > 0 {
		number, wireType, tagLength := protowire.ConsumeTag(message)
		if tagLength < 0 {
			return false
		}
		message = message[tagLength:]
		path := strconv.Itoa(int(number))
		if prefix != "" {
			path = prefix + "." + path
		}
		if wireType != protowire.BytesType {
			valueLength := protowire.ConsumeFieldValue(number, wireType, message)
			if valueLength < 0 {
				return false
			}
			message = message[valueLength:]
			continue
		}
		value, valueLength := protowire.ConsumeBytes(message)
		if valueLength < 0 {
			return false
		}
		message = message[valueLength:]

		// A nested message is tried first: printable text rarely parses as
		// protobuf all the way through, but short messages can look like text.
		if depth < maxProtoDepth && len(value) > 0 && !looksLikeURL(value) {
			var nested []string
			if collectProtoText(value, path, depth+1, urls, &nested) {
				*textPaths = append(*textPaths, nested...)
				continue
			}
		}
		if isPrintableText(value) {
			*textPaths = append(*textPaths, path)
			if looksLikeURL(value) {
				if _, seen := urls[path]; !seen {
					urls[path] = string(value)
				}
			}
		}
	}
	return true
}

func looksLikeURL(value []byte) bool {
	return bytes.HasPrefix(value, []byte("https://")) && isPrintableText(value)
}

func isPrintableText(value []byte) bool {
	if len(value) == 0 || !utf8.Valid(value) {
		return false
	}
	for _, r := range string(value) {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

// describeFieldPaths lists field paths for an error message.
func describeFieldPaths(paths []string) string {
	if len(paths) == 0 {
		return "none"
	}
	const limit = 20
	if len(paths) > limit {
		return strings.Join(paths[:limit], ", ") + fmt.Sprintf(" and %d more", len(paths)-limit)
	}
	return strings.Join(paths, ", ")
}

// DownloadOriginal streams the original (unedited) file of a media key to
// destPath. When the item has edits, the original is still what the server
// returns at path 1.5.2.6.
func (a *Api) DownloadOriginal(mediaKey, destPath string) error {
	return a.DownloadOriginalWithProgress(context.Background(), mediaKey, destPath, nil)
}

// DownloadOriginalWithProgress is DownloadOriginal with cancellation through
// ctx and throttled progress reports. onProgress may be nil; total is -1 when
// the server does not announce the size.
func (a *Api) DownloadOriginalWithProgress(ctx context.Context, mediaKey, destPath string, onProgress func(done, total int64)) error {
	urls, err := a.GetDownloadURLs(mediaKey)
	if err != nil {
		return err
	}
	// Without an original, the edited file is what can be fetched; callers
	// comparing digests will see that it differs.
	url := urls.OriginalURL
	if url == "" {
		url = urls.EditedURL
	}
	if err := a.downloadToFile(ctx, url, destPath, onProgress); err != nil {
		return err
	}
	// Count the download toward the GUI's statistics.
	if info, statErr := os.Stat(destPath); statErr == nil {
		RecordDownload(info.Size())
	}
	return nil
}

func (a *Api) downloadToFile(ctx context.Context, url, destPath string, onProgress func(done, total int64)) error {
	// Signed Googleusercontent URLs take no Authorization header.
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", a.userAgent)

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("download request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download request failed with status %d", resp.StatusCode)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer func() { _ = out.Close() }()

	// Paced by the download speed limit.
	var body = NewLimitedReader(ctx, resp.Body, DownloadLimiter)
	if onProgress != nil {
		body = &downloadProgress{reader: body, total: resp.ContentLength, onProgress: onProgress}
	}
	if _, err := io.Copy(out, body); err != nil {
		return fmt.Errorf("failed to download file: %w", err)
	}
	return nil
}

// downloadProgress counts bytes arriving from the network. It is separate
// from ProgressReader, which counts disk reads for the upload graphs.
type downloadProgress struct {
	reader      io.Reader
	total       int64
	done        int64
	lastEmit    time.Time
	lastEmitted int64
	onProgress  func(done, total int64)
}

func (d *downloadProgress) Read(p []byte) (int, error) {
	n, err := d.reader.Read(p)
	d.done += int64(n)
	// Throttle updates, but always report the final count at the end.
	if d.done != d.lastEmitted && (err == io.EOF || time.Since(d.lastEmit) >= 100*time.Millisecond) {
		d.lastEmit = time.Now()
		d.lastEmitted = d.done
		d.onProgress(d.done, d.total)
	}
	return n, err
}
