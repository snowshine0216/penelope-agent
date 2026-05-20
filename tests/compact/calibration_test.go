package compact_test

import (
	"math"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/compact"
)

func TestNewCalibratorStartsAtRatioOne(t *testing.T) {
	c := compact.NewCalibrator(0.3)
	if c.Ratio() != 1.0 {
		t.Fatalf("initial ratio = %v, want 1.0", c.Ratio())
	}
}

func TestCalibratorPredictAtRatioOne(t *testing.T) {
	c := compact.NewCalibrator(0.3)
	if c.Predict(1000) != 1000 {
		t.Fatalf("predict at ratio 1 = %d, want 1000", c.Predict(1000))
	}
}

func TestCalibratorObserveZeroProviderIsIgnored(t *testing.T) {
	c := compact.NewCalibrator(0.3)
	c.Observe(1000, 0)
	if c.Ratio() != 1.0 {
		t.Fatalf("ratio after zero observe = %v, want 1.0", c.Ratio())
	}
}

func TestCalibratorObserveZeroLocalIsIgnored(t *testing.T) {
	c := compact.NewCalibrator(0.3)
	c.Observe(0, 1500)
	if c.Ratio() != 1.0 {
		t.Fatalf("ratio after zero local observe = %v, want 1.0", c.Ratio())
	}
}

func TestCalibratorEWMAConverges(t *testing.T) {
	c := compact.NewCalibrator(0.3)
	// Local estimate is consistently low: provider reports 1.5x more.
	for range 20 {
		c.Observe(1000, 1500)
	}
	if math.Abs(c.Ratio()-1.5) > 0.01 {
		t.Fatalf("ratio after 20 obs = %v, want ~1.5", c.Ratio())
	}
}

func TestCalibratorPredictUsesRatio(t *testing.T) {
	c := compact.NewCalibrator(1.0) // alpha=1 means each observation overwrites
	c.Observe(1000, 1500)
	if c.Ratio() != 1.5 {
		t.Fatalf("ratio = %v, want 1.5", c.Ratio())
	}
	if c.Predict(2000) != 3000 {
		t.Fatalf("predict(2000) = %d, want 3000", c.Predict(2000))
	}
}

func TestCalibratorDefaultAlpha(t *testing.T) {
	c := compact.NewCalibrator(0) // 0 -> default 0.3
	if c.Alpha() != 0.3 {
		t.Fatalf("alpha = %v, want 0.3 default", c.Alpha())
	}
}
