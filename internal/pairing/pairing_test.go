package pairing

import (
	"bytes"
	"testing"
	"time"

	"myapp/internal/identity"
)

func TestBothSidesProduceSameCodeAndConfirmation(t *testing.T) {
	now := time.Unix(1700000000, 0)
	aIdentity, err := identity.Generate("主电脑", now)
	if err != nil {
		t.Fatal(err)
	}
	bIdentity, err := identity.Generate("子电脑", now)
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(aIdentity, now)
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(bIdentity, now)
	if err != nil {
		t.Fatal(err)
	}
	aMatch, err := a.Match(b.Offer, now)
	if err != nil {
		t.Fatal(err)
	}
	bMatch, err := b.Match(a.Offer, now)
	if err != nil {
		t.Fatal(err)
	}
	if aMatch.Code != bMatch.Code || len(aMatch.Code) != 6 || !bytes.Equal(aMatch.Confirmation, bMatch.Confirmation) {
		t.Fatalf("双方结果不同: a=%+v b=%+v", aMatch, bMatch)
	}
	if aMatch.RemoteDeviceID != bIdentity.DeviceID || bMatch.RemoteDeviceID != aIdentity.DeviceID {
		t.Fatal("远端身份错误")
	}
}

func TestOfferTamperingAndExpiry(t *testing.T) {
	now := time.Unix(1700000000, 0)
	localIdentity, err := identity.Generate("电脑", now)
	if err != nil {
		t.Fatal(err)
	}
	local, err := New(localIdentity, now)
	if err != nil {
		t.Fatal(err)
	}
	tampered := local.Offer
	tampered.DeviceName = "伪造名称"
	if err := ValidateOffer(tampered, now); err == nil {
		t.Fatal("篡改配对信息未被拒绝")
	}
	if err := ValidateOffer(local.Offer, now.Add(OfferLifetime+time.Second)); err == nil {
		t.Fatal("过期配对信息未被拒绝")
	}
}
