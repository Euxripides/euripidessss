package contractintelligence

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func addressSlot(last byte) [32]byte {
	var value [32]byte
	value[31] = last
	return value
}

func TestDetectProxyFamilies(t *testing.T) {
	implementation := "0x0000000000000000000000000000000000000011"
	minimal := append(append(append([]byte{}, minimalPrefix...), bytes.Repeat([]byte{0}, 19)...), 0x11)
	minimal = append(minimal, minimalSuffix...)
	tests := []struct {
		name     string
		evidence ProxyEvidence
		kind     ProxyType
		impl     string
		beacon   string
	}{
		{name: "minimal", evidence: ProxyEvidence{RuntimeCode: minimal}, kind: ProxyMinimal1167, impl: implementation},
		{name: "beacon", evidence: ProxyEvidence{Storage: map[StorageSlot][32]byte{BeaconSlot: addressSlot(0x22)}}, kind: ProxyBeacon, beacon: "0x0000000000000000000000000000000000000022"},
		{name: "transparent storage", evidence: ProxyEvidence{Storage: map[StorageSlot][32]byte{ImplementationSlot: addressSlot(0x11), AdminSlot: addressSlot(0x33)}}, kind: ProxyTransparent, impl: implementation},
		{name: "transparent selector", evidence: ProxyEvidence{RuntimeCode: []byte{0xf8, 0x51, 0xa4, 0x40}, Storage: map[StorageSlot][32]byte{ImplementationSlot: addressSlot(0x11)}}, kind: ProxyTransparent, impl: implementation},
		{name: "uups implementation evidence", evidence: ProxyEvidence{ImplementationRuntimeCode: []byte{0x52, 0xd1, 0x90, 0x2d}, Storage: map[StorageSlot][32]byte{ImplementationSlot: addressSlot(0x11)}}, kind: ProxyUUPS, impl: implementation},
		{name: "eip1967", evidence: ProxyEvidence{Storage: map[StorageSlot][32]byte{ImplementationSlot: addressSlot(0x11)}}, kind: ProxyEIP1967, impl: implementation},
		{name: "not proxy", evidence: ProxyEvidence{RuntimeCode: []byte{0x60, 0x00}}, kind: ProxyNone},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := DetectProxy(test.evidence)
			if got.ProxyType != test.kind || got.ImplementationAddress != test.impl || got.BeaconAddress != test.beacon || got.IsProxy != (test.kind != ProxyNone) {
				t.Fatalf("DetectProxy()=%+v", got)
			}
		})
	}
}

func TestMinimalProxyRejectsNearMiss(t *testing.T) {
	code := append(append(append([]byte{}, minimalPrefix...), bytes.Repeat([]byte{1}, 20)...), minimalSuffix...)
	code[0] = 0x37
	if got := DetectProxy(ProxyEvidence{RuntimeCode: code}); got.IsProxy {
		t.Fatalf("near miss detected as proxy: %+v", got)
	}
}

type evidenceStub struct {
	code       []byte
	storage    map[StorageSlot][32]byte
	beaconImpl string
	err        error
}

func (e evidenceStub) RuntimeCode(context.Context, uint32, string) ([]byte, error) {
	return e.code, e.err
}
func (e evidenceStub) StorageAt(_ context.Context, _ uint32, _ string, slot StorageSlot) ([32]byte, error) {
	return e.storage[slot], e.err
}
func (e evidenceStub) BeaconImplementation(context.Context, uint32, string) (string, error) {
	return e.beaconImpl, e.err
}

func TestInspectProxyUsesInjectedEvidenceAndBeaconResolver(t *testing.T) {
	reader := evidenceStub{storage: map[StorageSlot][32]byte{BeaconSlot: addressSlot(0x22)}, beaconImpl: "0x0000000000000000000000000000000000000033"}
	got, err := NewRepository(nil, reader).InspectProxy(context.Background(), 56, "0x0000000000000000000000000000000000000011")
	if err != nil || got.ProxyType != ProxyBeacon || got.ImplementationAddress != reader.beaconImpl {
		t.Fatalf("InspectProxy()=%+v err=%v", got, err)
	}
}

func TestInspectProxyDesensitizesReaderErrors(t *testing.T) {
	secret := errors.New("rpc URL has secret-token")
	_, err := NewRepository(nil, evidenceStub{err: secret}).InspectProxy(context.Background(), 56, "0x0000000000000000000000000000000000000011")
	if !errors.Is(err, ErrEvidenceRead) || errors.Is(err, secret) || err.Error() != ErrEvidenceRead.Error() {
		t.Fatalf("error was not sanitized: %v", err)
	}
}

func TestNormalizeCreationType(t *testing.T) {
	tests := []struct {
		raw, factory string
		proxy, token bool
		want         CreationType
	}{
		{raw: "create", want: CreationCreate}, {raw: "CREATE_2", want: CreationCreate2},
		{raw: "create", factory: "0x1", want: CreationFactory},
		{raw: "create2", factory: "0x1", proxy: true, want: CreationProxy},
		{raw: "proxy", token: true, want: CreationToken},
	}
	for _, test := range tests {
		if got := NormalizeCreationType(test.raw, test.factory, test.proxy, test.token); got != test.want {
			t.Fatalf("NormalizeCreationType(%q)=%s want %s", test.raw, got, test.want)
		}
	}
}
