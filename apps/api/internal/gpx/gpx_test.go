package gpx

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

const twoPointGPX = `<?xml version="1.0"?>
<gpx version="1.1" xmlns="http://www.topografix.com/GPX/1/1">
  <trk><trkseg>
    <trkpt lat="50.0000" lon="3.0000"><ele>10.0</ele></trkpt>
    <trkpt lat="50.0100" lon="3.0000"><ele>30.0</ele></trkpt>
  </trkseg></trk>
</gpx>`

func writeGPX(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "route.gpx")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadPointsAcceptsRouteElement(t *testing.T) {
	path := writeGPX(t, `<?xml version="1.0"?>
<gpx version="1.1" xmlns="http://www.topografix.com/GPX/1/1">
  <rte>
    <rtept lat="50.0" lon="3.0"/>
    <rtept lat="50.1" lon="3.0"/>
  </rte>
</gpx>`)

	points, err := ReadPoints(path)
	if err != nil {
		t.Fatalf("ReadPoints: %v", err)
	}
	if len(points) != 2 {
		t.Fatalf("got %d points, want 2", len(points))
	}
}

func TestReadPointsRejectsTooFewPoints(t *testing.T) {
	path := writeGPX(t, `<?xml version="1.0"?>
<gpx version="1.1" xmlns="http://www.topografix.com/GPX/1/1">
  <trk><trkseg><trkpt lat="50.0" lon="3.0"/></trkseg></trk>
</gpx>`)

	if _, err := ReadPoints(path); err == nil {
		t.Fatal("expected an error for a single-point GPX")
	}
}

func TestComputeStats(t *testing.T) {
	points, err := ReadPoints(writeGPX(t, twoPointGPX))
	if err != nil {
		t.Fatal(err)
	}
	stats := ComputeStats(points)

	// 0.01 degrees of latitude is ~1112 m.
	if math.Abs(stats.DistanceM-1112) > 15 {
		t.Errorf("distance = %.1f m, want ~1112 m", stats.DistanceM)
	}
	if math.Abs(stats.AscentM-20) > 0.01 {
		t.Errorf("ascent = %.1f m, want 20 m", stats.AscentM)
	}
	if stats.StartLat != 50.0 || stats.StartLng != 3.0 {
		t.Errorf("start = %v,%v, want 50,3", stats.StartLat, stats.StartLng)
	}
}

func TestComputeStatsIgnoresElevationNoise(t *testing.T) {
	// A dead-flat track with sub-threshold jitter must report no ascent.
	points := []Point{
		{Lat: 50, Lon: 3, Ele: 10, HasEle: true},
		{Lat: 50.001, Lon: 3, Ele: 11.5, HasEle: true},
		{Lat: 50.002, Lon: 3, Ele: 10.2, HasEle: true},
		{Lat: 50.003, Lon: 3, Ele: 11.8, HasEle: true},
	}
	if got := ComputeStats(points).AscentM; got != 0 {
		t.Errorf("ascent = %.2f m, want 0 from noise alone", got)
	}
}

func TestContentHashIgnoresPrecisionBelowOneMetre(t *testing.T) {
	base := []Point{{Lat: 50.123456789, Lon: 3.1234567}, {Lat: 50.2, Lon: 3.2}}
	jittered := []Point{{Lat: 50.1234561, Lon: 3.1234569}, {Lat: 50.2, Lon: 3.2}}

	if ContentHash(base, "Loop", "") != ContentHash(jittered, "Loop", "") {
		t.Error("hash changed on sub-metre coordinate jitter; re-exports would churn")
	}
	if ContentHash(base, "Loop", "") == ContentHash(base, "Other Loop", "") {
		t.Error("hash ignored the route name; renames would not propagate")
	}
}
