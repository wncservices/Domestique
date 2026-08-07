package gpx

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/wncservices/domestique/apps/api/internal/model"
)

const (
	// ascentThresholdM ignores elevation wobble below this when summing ascent.
	// GPS barometers are noisy; without it a flat ride reports a few hundred
	// metres of climbing.
	ascentThresholdM = 3.0

	// hashPrecision is the coordinate precision used for hashing: ~1m, well
	// below any meaningful route edit.
	hashPrecision = 5

	earthRadiusM = 6371000.0
)

// Point is a single track point. Elevation is optional.
type Point struct {
	Lat, Lon float64
	Ele      float64
	HasEle   bool
}

// gpxDoc mirrors just enough of the GPX schema to read a planned route.
type gpxDoc struct {
	XMLName xml.Name `xml:"gpx"`
	Tracks  []struct {
		Segments []struct {
			Points []gpxPoint `xml:"trkpt"`
		} `xml:"trkseg"`
	} `xml:"trk"`
	Routes []struct {
		Points []gpxPoint `xml:"rtept"`
	} `xml:"rte"`
}

type gpxPoint struct {
	Lat float64 `xml:"lat,attr"`
	Lon float64 `xml:"lon,attr"`
	Ele *string `xml:"ele"`
}

func (p gpxPoint) toPoint() Point {
	out := Point{Lat: p.Lat, Lon: p.Lon}
	if p.Ele != nil {
		var ele float64
		if _, err := fmt.Sscanf(strings.TrimSpace(*p.Ele), "%g", &ele); err == nil {
			out.Ele, out.HasEle = ele, true
		}
	}
	return out
}

// ReadPoints flattens a GPX file on disk into a single ordered point list.
func ReadPoints(path string) ([]Point, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	points, err := ParsePoints(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return points, nil
}

// ParsePoints flattens GPX bytes into a single ordered point list.
//
// It accepts tracks or routes: planners disagree about which element a planned
// route belongs in, and we treat both as the same thing.
func ParsePoints(raw []byte) ([]Point, error) {
	var doc gpxDoc
	if err := xml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("could not parse GPX: %w", err)
	}

	var points []Point
	for _, track := range doc.Tracks {
		for _, seg := range track.Segments {
			for _, p := range seg.Points {
				points = append(points, p.toPoint())
			}
		}
	}
	if len(points) == 0 {
		for _, route := range doc.Routes {
			for _, p := range route.Points {
				points = append(points, p.toPoint())
			}
		}
	}

	if len(points) < 2 {
		return nil, fmt.Errorf("needs at least 2 track points, found %d", len(points))
	}
	return points, nil
}

// ComputeStats derives the metrics providers ask for at create time.
func ComputeStats(points []Point) model.RouteStats {
	var distance, ascent float64
	for i := 1; i < len(points); i++ {
		distance += haversineM(points[i-1], points[i])
	}

	var lastEle float64
	haveLast := false
	for _, p := range points {
		if !p.HasEle {
			continue
		}
		if !haveLast {
			lastEle, haveLast = p.Ele, true
			continue
		}
		switch delta := p.Ele - lastEle; {
		case delta >= ascentThresholdM:
			ascent += delta
			lastEle = p.Ele
		case delta <= -ascentThresholdM:
			lastEle = p.Ele
		}
	}

	return model.RouteStats{
		DistanceM:  distance,
		AscentM:    ascent,
		StartLat:   points[0].Lat,
		StartLng:   points[0].Lon,
		PointCount: len(points),
	}
}

// ContentHash is a stable hash of what a provider would actually see.
//
// It deliberately excludes timestamps, extensions and whitespace, so
// re-exporting the same route from a different planner is not a change.
func ContentHash(points []Point, name, description string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s", strings.TrimSpace(name), strings.TrimSpace(description))
	for _, p := range points {
		fmt.Fprintf(h, "\x00%.*f,%.*f", hashPrecision, p.Lat, hashPrecision, p.Lon)
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

func haversineM(a, b Point) float64 {
	p1, p2 := a.Lat*math.Pi/180, b.Lat*math.Pi/180
	dp := p2 - p1
	dl := (b.Lon - a.Lon) * math.Pi / 180
	x := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 2 * earthRadiusM * math.Asin(math.Sqrt(x))
}
