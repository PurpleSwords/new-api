package model

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupChannelWebSocketCapabilityTestDB(t *testing.T) {
	t.Helper()
	previousDB := DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&Channel{}); err != nil {
		t.Fatalf("migrate channels: %v", err)
	}
	DB = db
	t.Cleanup(func() { DB = previousDB })
}

func TestUpdateChannelResponsesWebSocketCapabilityPreservesOtherSettingsAndKey(t *testing.T) {
	setupChannelWebSocketCapabilityTestDB(t)
	channel := &Channel{
		Name:          "test",
		Key:           "secret-key",
		OtherSettings: `{"allow_service_tier":true,"future_extension":{"enabled":true}}`,
	}
	if err := DB.Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}

	updated, err := UpdateChannelResponsesWebSocketCapability(channel.Id, true)
	if err != nil {
		t.Fatalf("update capability: %v", err)
	}
	settings := updated.GetOtherSettings()
	if settings.SupportsResponsesWebSocket == nil || !*settings.SupportsResponsesWebSocket {
		t.Fatalf("supports_responses_websocket = %v, want true", settings.SupportsResponsesWebSocket)
	}
	if !settings.AllowServiceTier {
		t.Fatal("allow_service_tier was not preserved")
	}
	if updated.Key != "secret-key" {
		t.Fatalf("key = %q, want preserved key", updated.Key)
	}

	var stored Channel
	if err := DB.First(&stored, channel.Id).Error; err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	storedSettings := dto.ChannelOtherSettings{}
	if err := json.Unmarshal([]byte(stored.OtherSettings), &storedSettings); err != nil {
		t.Fatalf("unmarshal stored settings: %v", err)
	}
	if storedSettings.SupportsResponsesWebSocket == nil || !*storedSettings.SupportsResponsesWebSocket {
		t.Fatalf("stored supports_responses_websocket = %v, want true", storedSettings.SupportsResponsesWebSocket)
	}
	var rawSettings map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stored.OtherSettings), &rawSettings); err != nil {
		t.Fatalf("unmarshal raw stored settings: %v", err)
	}
	if got := string(rawSettings["future_extension"]); got != `{"enabled":true}` {
		t.Fatalf("future_extension = %s, want preserved value", got)
	}
}

func TestUpdateChannelResponsesWebSocketCapabilityRejectsInvalidSettings(t *testing.T) {
	setupChannelWebSocketCapabilityTestDB(t)
	channel := &Channel{Name: "invalid", Key: "secret-key", OtherSettings: "{"}
	if err := DB.Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}

	if _, err := UpdateChannelResponsesWebSocketCapability(channel.Id, true); err == nil {
		t.Fatal("expected invalid settings error")
	}
	var stored Channel
	if err := DB.First(&stored, channel.Id).Error; err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if stored.OtherSettings != "{" {
		t.Fatalf("settings = %q, want unchanged invalid value", stored.OtherSettings)
	}
}
