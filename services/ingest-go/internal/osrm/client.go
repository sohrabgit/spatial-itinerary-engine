// Package osrm talks to the local routing engine.
package osrm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	base string
	http *http.Client
}

func New(base string) *Client {
	return &Client{
		base: base,
		// Connection reuse matters: the snap stage makes one request per POI.
		http: &http.Client{
			Timeout: 20 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        64,
				MaxIdleConnsPerHost: 64,
				IdleConnTimeout:     30 * time.Second,
			},
		},
	}
}

type nearestResp struct {
	Code      string `json:"code"`
	Waypoints []struct {
		Location [2]float64 `json:"location"` // [lon, lat]
		Distance float64    `json:"distance"` // metres from the input point
		Name     string     `json:"name"`
	} `json:"waypoints"`
}

// Snap is the routable point OSRM would actually start from.
type Snap struct {
	Lon, Lat float64
	DistM    float64
	Street   string
}

// Nearest snaps a coordinate onto the routing graph.
//
// This exists because POI coordinates are building centroids, not street
// points. For large complexes -- the Louvre, Pere-Lachaise, Bois de Boulogne --
// the snap can land 200m+ away, silently corrupting every duration in the
// matrix. Routing uses snap_geog; display uses geog.
func (c *Client) Nearest(ctx context.Context, lon, lat float64) (Snap, error) {
	url := fmt.Sprintf("%s/nearest/v1/foot/%.7f,%.7f?number=1", c.base, lon, lat)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Snap{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Snap{}, fmt.Errorf("osrm nearest: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return Snap{}, fmt.Errorf("osrm nearest: http %d", resp.StatusCode)
	}
	var out nearestResp
	if err := json.Unmarshal(body, &out); err != nil {
		return Snap{}, fmt.Errorf("osrm nearest: decode: %w", err)
	}
	if out.Code != "Ok" || len(out.Waypoints) == 0 {
		return Snap{}, fmt.Errorf("osrm nearest: code=%s", out.Code)
	}
	w := out.Waypoints[0]
	return Snap{Lon: w.Location[0], Lat: w.Location[1], DistM: w.Distance, Street: w.Name}, nil
}

func (c *Client) Health(ctx context.Context) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		c.base+"/route/v1/foot/2.3364,48.8606;2.3376,48.8530", nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("osrm unreachable at %s: %w", c.base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("osrm health: http %d", resp.StatusCode)
	}
	return nil
}
