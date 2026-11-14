package core

import (
	"bytes"
	"fmt"
	"net/http"

	"app/generated"

	"google.golang.org/protobuf/proto"
)

// MediaStateEndpoint is the media-state mutation RPC shared by move-to-trash,
// permanent delete and restore-from-trash.
const MediaStateEndpoint = "https://photosdata-pa.googleapis.com/6439526531001121323/17490284929287180316"

// Media-state actions and confirmations observed in the mobile app.
const (
	mediaStateActionTrash   int64 = 1
	mediaStateActionDelete  int64 = 2
	mediaStateActionRestore int64 = 3

	mediaStateConfirmTrash int64 = 1
	// Delete and restore both confirm with 2.
	mediaStateConfirmDelete int64 = 2
)

// mediaStateUpdate builds the request for one media-state mutation. All
// three actions share the same endpoint and message shape; they differ in
// action/confirm codes and in which optional groups are present.
func mediaStateUpdate(action, confirm int64, trashExtras bool, clientVersionCode, androidAPIVersion int64, dedupKeys []string) *generated.MediaStateUpdate {
	protoBody := generated.MediaStateUpdate{
		Action:    action,
		DedupKeys: dedupKeys,
		Confirm:   confirm,
		Field8: &generated.MediaStateUpdateField8Type{
			Field4: &generated.MediaStateUpdateField8TypeField4Type{
				// Empty sub-messages, present on the wire as zero-length
				// fields; trash and permanent delete send all four, restore
				// only field2 and field3.
				Field2: &generated.MediaStateUpdateField8TypeField4TypeField2Type{},
				Field3: &generated.MediaStateUpdateField8TypeField4TypeField3Type{
					Field1: &generated.MediaStateUpdateField8TypeField4TypeField3TypeField1Type{},
				},
			},
		},
	}
	if trashExtras {
		protoBody.Field8.Field4.Field4 = &generated.MediaStateUpdateField8TypeField4TypeField4Type{}
		protoBody.Field8.Field4.Field5 = &generated.MediaStateUpdateField8TypeField4TypeField5Type{
			Field1: &generated.MediaStateUpdateField8TypeField4TypeField5TypeField1Type{},
		}
	}
	if action == mediaStateActionDelete {
		// Permanent delete sends the client-version field as an empty
		// message.
		protoBody.Field9 = &generated.MediaStateUpdateField9Type{}
	} else {
		protoBody.Field9 = &generated.MediaStateUpdateField9Type{
			Field1: 5,
			Field2: &generated.MediaStateUpdateField9TypeField2Type{
				Field1: clientVersionCode,
				Field2: fmt.Sprintf("%d", androidAPIVersion),
			},
		}
	}
	return &protoBody
}

// updateMediaState sends one media-state mutation for the given dedup keys.
//
// Safety notes:
//   - Items in the trash still count against the storage quota.
//   - Permanent delete must be preceded by moving the item to the trash;
//     deleting an item that was never trashed fails because the server
//     matches items by their SHA-1 derived dedup key.
//   - Restore-from-trash is the safety net to undo a trash or a mistaken
//     permanent delete of a still-trashed item.
func (a *Api) updateMediaState(action, confirm int64, trashExtras bool, dedupKeys []string) error {
	serializedData, err := proto.Marshal(
		mediaStateUpdate(action, confirm, trashExtras, a.clientVersionCode, a.androidAPIVersion, dedupKeys),
	)
	if err != nil {
		return fmt.Errorf("failed to marshal protobuf: %w", err)
	}

	bearerToken, err := a.BearerToken()
	if err != nil {
		return fmt.Errorf("failed to get bearer token: %w", err)
	}

	// The media-state RPCs send no x-goog-ext headers, unlike the library
	// sync and download RPCs.
	req, err := http.NewRequest("POST", MediaStateEndpoint, bytes.NewReader(serializedData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("Accept-Language", a.language)
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("User-Agent", a.userAgent)
	req.Header.Set("Authorization", "Bearer "+bearerToken)

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := ReadResponseBody(resp)
		return fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}
	if _, err := ReadResponseBody(resp); err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}
	return nil
}

// MoveRemoteMediaToTrash moves remote media items to the trash by their dedup
// keys. Trashed items keep counting against the storage quota until they are
// deleted permanently.
func (a *Api) MoveRemoteMediaToTrash(dedupKeys []string) error {
	return a.updateMediaState(mediaStateActionTrash, mediaStateConfirmTrash, true, dedupKeys)
}

// DeleteRemoteMediaPermanently deletes remote media items permanently by
// their dedup keys. The items must be in the trash already.
func (a *Api) DeleteRemoteMediaPermanently(dedupKeys []string) error {
	return a.updateMediaState(mediaStateActionDelete, mediaStateConfirmDelete, true, dedupKeys)
}

// RestoreMediaFromTrash restores remote media items from the trash by their
// dedup keys.
func (a *Api) RestoreMediaFromTrash(dedupKeys []string) error {
	return a.updateMediaState(mediaStateActionRestore, mediaStateConfirmDelete, false, dedupKeys)
}
