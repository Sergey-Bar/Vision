// Package sitemodel loads and provides site topology for the autonomy layer.
package sitemodel

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// loaderYAML is the YAML structure for loading a site.
type loaderYAML struct {
	Site struct {
		ID      string `yaml:"id"`
		Cameras []struct {
			ID       string   `yaml:"id"`
			ZoneIDs  []string `yaml:"zone_ids"`
			Position struct {
				X float64 `yaml:"x"`
				Y float64 `yaml:"y"`
			} `yaml:"position"`
			FieldOfView *struct {
				BearingDegrees *float64 `yaml:"bearing_degrees"`
				ExtentDegrees  *float64 `yaml:"extent_degrees"`
				RangeMetres    *float64 `yaml:"range_metres"`
			} `yaml:"field_of_view"`
		} `yaml:"cameras"`
		Zones []struct {
			ID      string `yaml:"id"`
			Type    string `yaml:"type"`
			Polygon []struct {
				X float64 `yaml:"x"`
				Y float64 `yaml:"y"`
			} `yaml:"polygon"`
			Name string `yaml:"name"`
		} `yaml:"zones"`
		Routes []struct {
			ID     string `yaml:"id"`
			Points []struct {
				X float64 `yaml:"x"`
				Y float64 `yaml:"y"`
			} `yaml:"points"`
			AllowedDirection string `yaml:"allowed_direction"`
		} `yaml:"routes"`
		Assets []struct {
			ID       string `yaml:"id"`
			Name     string `yaml:"name"`
			Type     string `yaml:"type"`
			ZoneID   string `yaml:"zone_id"`
			Position struct {
				X float64 `yaml:"x"`
				Y float64 `yaml:"y"`
			} `yaml:"position"`
			Metadata map[string]any `yaml:"metadata"`
		} `yaml:"assets"`
	} `yaml:"site"`
}

// LoadFromFile loads a site from a YAML file.
func LoadFromFile(path string) (*Site, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("sitemodel: read file: %w", err)
	}
	return LoadFromBytes(data)
}

// LoadFromBytes loads a site from YAML bytes.
func LoadFromBytes(data []byte) (*Site, error) {
	var ly loaderYAML
	if err := yaml.Unmarshal(data, &ly); err != nil {
		return nil, fmt.Errorf("sitemodel: unmarshal yaml: %w", err)
	}
	return ly.toSite()
}

// cameraFieldOfView maps the YAML's optional view block, keeping nil as nil.
//
// The three inner pointers are nil when the site did not write the key, so a
// camera that declares only a bearing has a view whose extent is unknown — and
// adjacency.go answers that as "unknown", not as "sees everything".
func cameraFieldOfView(f *struct {
	BearingDegrees *float64 `yaml:"bearing_degrees"`
	ExtentDegrees  *float64 `yaml:"extent_degrees"`
	RangeMetres    *float64 `yaml:"range_metres"`
}) *FieldOfView {
	if f == nil {
		return nil
	}
	return &FieldOfView{
		BearingDegrees: f.BearingDegrees,
		ExtentDegrees:  f.ExtentDegrees,
		RangeMetres:    f.RangeMetres,
	}
}

func (ly *loaderYAML) toSite() (*Site, error) {
	if ly.Site.ID == "" {
		return nil, errors.New("sitemodel: site id is required")
	}
	site := &Site{
		ID: ly.Site.ID,
	}
	for _, c := range ly.Site.Cameras {
		if c.ID == "" {
			return nil, errors.New("sitemodel: camera id is required")
		}
		site.Cameras = append(site.Cameras, Camera{
			ID:       c.ID,
			ZoneIDs:  c.ZoneIDs,
			Position: Point{X: c.Position.X, Y: c.Position.Y},
			// An undeclared view is nil, not a zero view. A site that has not
			// measured its cameras' bearings gets no derived overlap rather
			// than a false one.
			FieldOfView: cameraFieldOfView(c.FieldOfView),
		})
	}
	for _, z := range ly.Site.Zones {
		if z.ID == "" {
			return nil, errors.New("sitemodel: zone id is required")
		}
		if len(z.Polygon) < 3 {
			return nil, fmt.Errorf("sitemodel: zone %s must have at least 3 polygon points", z.ID)
		}
		poly := make([]Point, len(z.Polygon))
		for i, p := range z.Polygon {
			poly[i] = Point{X: p.X, Y: p.Y}
		}
		site.Zones = append(site.Zones, Zone{
			ID:      z.ID,
			Type:    z.Type,
			Polygon: poly,
			Name:    z.Name,
		})
	}
	for _, r := range ly.Site.Routes {
		if r.ID == "" {
			return nil, errors.New("sitemodel: route id is required")
		}
		if len(r.Points) < 2 {
			return nil, fmt.Errorf("sitemodel: route %s must have at least 2 points", r.ID)
		}
		points := make([]Point, len(r.Points))
		for i, p := range r.Points {
			points[i] = Point{X: p.X, Y: p.Y}
		}
		dir := r.AllowedDirection
		if dir == "" {
			dir = "both"
		}
		site.Routes = append(site.Routes, Route{
			ID:               r.ID,
			Points:           points,
			AllowedDirection: dir,
		})
	}
	for _, a := range ly.Site.Assets {
		if a.ID == "" {
			return nil, errors.New("sitemodel: asset id is required")
		}
		site.Assets = append(site.Assets, Asset{
			ID:       a.ID,
			Name:     a.Name,
			Type:     a.Type,
			ZoneID:   a.ZoneID,
			Position: Point{X: a.Position.X, Y: a.Position.Y},
			Metadata: a.Metadata,
		})
	}
	return site, nil
}
