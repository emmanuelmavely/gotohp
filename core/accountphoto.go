package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// accountPhotoMaxAge is how long a saved account photo is shown before it is
// fetched again.
const accountPhotoMaxAge = 24 * time.Hour

// accountPhotoSize is the edge in pixels the photo is requested at.
const accountPhotoSize = 96

const userInfoURL = "https://www.googleapis.com/oauth2/v3/userinfo"

// AccountPhotoPath is where an account's photo is kept, next to the config.
func AccountPhotoPath(email string) string {
	if ConfigPath == "" || email == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(ConfigPath), "avatars", safeEmailFileName(email)+".jpg")
}

var unsafeFileChars = regexp.MustCompile(`[^A-Za-z0-9@._+-]`)

func safeEmailFileName(email string) string {
	return unsafeFileChars.ReplaceAllString(strings.ToLower(strings.TrimSpace(email)), "_")
}

// AccountPhotoDataURL returns the account's photo as a data: URL for the
// window to show. A saved copy younger than a day is used as it is; an
// older or missing one is fetched again, and when that fails the old copy
// (if any) is still returned. "" means there is no photo.
func AccountPhotoDataURL(ctx context.Context, email string, proxy string) (string, error) {
	path := AccountPhotoPath(email)
	if path == "" {
		return "", fmt.Errorf("no account")
	}
	if info, err := os.Stat(path); err == nil && time.Since(info.ModTime()) < accountPhotoMaxAge {
		return photoFileDataURL(path)
	}
	fetchErr := fetchAccountPhoto(ctx, email, proxy, path)
	if dataURL, err := photoFileDataURL(path); err == nil {
		return dataURL, nil
	}
	return "", fetchErr
}

func photoFileDataURL(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return "", fmt.Errorf("no saved photo")
	}
	return "data:" + http.DetectContentType(data) + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

// fetchAccountPhoto asks Google who the signed-in account is (the sign-in
// carries the openid scope, which returns the picture) and saves the photo.
func fetchAccountPhoto(ctx context.Context, email string, proxy string, path string) error {
	api, err := NewApi(ApiOptions{Account: email, Proxy: proxy})
	if err != nil {
		return err
	}
	token, err := api.BearerToken()
	if err != nil {
		return err
	}
	pictureURL, err := userInfoPicture(ctx, api.client, token)
	if err != nil {
		return err
	}
	if pictureURL == "" {
		return fmt.Errorf("the account has no photo")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, SizedAccountPhotoURL(pictureURL, accountPhotoSize), nil)
	if err != nil {
		return err
	}
	response, err := api.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("photo download returned %s", response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return err
	}
	if !strings.HasPrefix(http.DetectContentType(data), "image/") {
		return fmt.Errorf("the photo is not an image")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func userInfoPicture(ctx context.Context, client *http.Client, token string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, userInfoURL, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("userinfo returned %s", response.Status)
	}
	var info struct {
		Picture string `json:"picture"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&info); err != nil {
		return "", err
	}
	return info.Picture, nil
}

var photoSizeSuffix = regexp.MustCompile(`=s\d+(-c)?$`)

// SizedAccountPhotoURL asks for the photo at size pixels, cropped square.
func SizedAccountPhotoURL(pictureURL string, size int) string {
	suffix := fmt.Sprintf("=s%d-c", size)
	if photoSizeSuffix.MatchString(pictureURL) {
		return photoSizeSuffix.ReplaceAllString(pictureURL, suffix)
	}
	return pictureURL + suffix
}
