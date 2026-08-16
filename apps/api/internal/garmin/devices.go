package garmin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"
)

// devicesPath is what Connect's own Devices page asks for.
//
// Undocumented like everything else here, so a failure is "the device list
// could not be read" rather than "the connection is broken" — a rider whose
// courses sync perfectly well does not need a broken screen because Garmin
// moved a URL.
const devicesPath = "/device-service/deviceregistration/devices"

// Device is one registered head unit.
//
// Deliberately small. Connect returns a great deal about a device — serial
// numbers, firmware, part numbers — and none of it belongs in this app's
// database or on its screens. A name and when it last synced is what a rider
// needs to recognise their own unit.
type Device struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	LastSync time.Time `json:"lastSync,omitzero"`
}

// deviceDTO is Connect's shape, which is wider and less consistent than ours.
type deviceDTO struct {
	DeviceID           json.Number `json:"deviceId"`
	ProductDisplayName string      `json:"productDisplayName"`
	DisplayName        string      `json:"displayName"`
	// Connect has answered with both spellings; neither is guaranteed.
	LastSyncMillis json.Number `json:"lastSyncTime"`
	LastUsedMillis json.Number `json:"lastUsedDeviceUploadTime"`
}

// name prefers the product name: "Edge 530" is what is printed on the unit,
// while displayName is often a serial-ish string nobody recognises.
func (d deviceDTO) name() string {
	if d.ProductDisplayName != "" {
		return d.ProductDisplayName
	}
	return d.DisplayName
}

func (d deviceDTO) lastSync() time.Time {
	for _, raw := range []json.Number{d.LastSyncMillis, d.LastUsedMillis} {
		if ms, err := strconv.ParseInt(raw.String(), 10, 64); err == nil && ms > 0 {
			return time.UnixMilli(ms).UTC()
		}
	}
	return time.Time{}
}

// Devices lists the head units registered to this account.
//
// Informational: a course is pushed to the *account*, and Connect syncs it to
// whichever units can take it. Showing them answers the question a rider
// actually has — "will this reach my Edge?" — which linking an account does
// not answer on its own.
func (c *Client) Devices(ctx context.Context) ([]Device, error) {
	bearer, err := c.bearerToken(ctx)
	if err != nil {
		return nil, err
	}

	raw, status, err := c.do(ctx, http.MethodGet, c.APIBase+devicesPath, nil, "",
		header{"Authorization", "Bearer " + bearer},
		header{"Accept", "application/json"},
		header{"X-Requested-With", "XMLHttpRequest"},
	)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("garmin: the device list returned %d: %s", status, snippet(raw))
	}

	var dtos []deviceDTO
	if err := json.Unmarshal(raw, &dtos); err != nil {
		return nil, fmt.Errorf("garmin: unreadable device list: %w", err)
	}

	out := make([]Device, 0, len(dtos))
	for _, d := range dtos {
		name := d.name()
		if name == "" {
			// A device with no name is a row nobody can act on.
			continue
		}
		out = append(out, Device{
			ID:       d.DeviceID.String(),
			Name:     name,
			LastSync: d.lastSync(),
		})
	}

	// Most recently synced first: the unit a rider is actually using is the
	// one they are looking for.
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastSync.After(out[j].LastSync) })
	return out, nil
}
