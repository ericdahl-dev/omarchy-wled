package serial_test

import (
	"testing"

	"github.com/ericdahl-dev/omarchy-wled/internal/serial"
)

func TestSerialTransportResolveGradientLEDCountConfigured(t *testing.T) {
	tr := &serial.Transport{Port: "/dev/ttyUSB0"}
	n, err := tr.ResolveGradientLEDCount(60)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 60 {
		t.Errorf("want 60, got %d", n)
	}
}

func TestSerialTransportResolveGradientLEDCountZeroErrors(t *testing.T) {
	tr := &serial.Transport{Port: "/dev/ttyUSB0"}
	_, err := tr.ResolveGradientLEDCount(0)
	if err == nil {
		t.Fatal("expected error when configured=0, got nil")
	}
}
