package presence

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func decodeJSON(t *testing.T, s string) map[string]any {
	var m map[string]any
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("failed to decode json: %v", err)
	}
	return m
}

func TestPresenceFixtures(t *testing.T) {
	t.Run("fixture (a): no presence", func(t *testing.T) {
		user := decodeJSON(t, `{"id": "123456789012345678", "username": "alice", "discriminator": "0001"}`)
		kv := map[string]string{"foo": "bar"}

		p := BuildPretty(user, nil, kv)
		data, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("marshal error: %v", err)
		}

		var m map[string]any
		json.Unmarshal(data, &m)

		if m["discord_status"] != "offline" {
			t.Errorf("expected offline, got %v", m["discord_status"])
		}
		if m["listening_to_spotify"] != false {
			t.Errorf("expected listening_to_spotify false")
		}
		if m["spotify"] != nil {
			t.Errorf("expected spotify null")
		}
		if acts, ok := m["activities"].([]any); !ok || len(acts) != 0 {
			t.Errorf("expected activities [], got %v", m["activities"])
		}
		if !bytes.Contains(data, []byte(`"activities":[]`)) {
			t.Errorf("expected 'activities:[]' in json: %s", string(data))
		}
		if !bytes.Contains(data, []byte(`"kv":{"foo":"bar"}`)) {
			t.Errorf("expected kv in json: %s", string(data))
		}
	})

	t.Run("fixture (b): online desktop", func(t *testing.T) {
		user := decodeJSON(t, `{"id": "123", "username": "bob"}`)
		presenceRaw := decodeJSON(t, `{
			"status": "online",
			"client_status": {"desktop": "online"},
			"activities": []
		}`)

		p := BuildPretty(user, presenceRaw, nil)
		data, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("marshal error: %v", err)
		}

		var m map[string]any
		json.Unmarshal(data, &m)

		if m["discord_status"] != "online" {
			t.Errorf("expected online, got %v", m["discord_status"])
		}
		if m["active_on_discord_desktop"] != true {
			t.Errorf("expected active_on_discord_desktop true")
		}
		if m["active_on_discord_web"] != false {
			t.Errorf("expected active_on_discord_web false")
		}
		// Empty kv must be {}
		if !bytes.Contains(data, []byte(`"kv":{}`)) {
			t.Errorf("expected 'kv:{}' in json: %s", string(data))
		}
	})

	t.Run("fixture (c): Spotify listening", func(t *testing.T) {
		user := decodeJSON(t, `{"id": "123", "username": "carol"}`)
		presenceRaw := decodeJSON(t, `{
			"status": "online",
			"client_status": {"desktop": "online"},
			"activities": [
				{
					"id": "spotify:1",
					"name": "Spotify",
					"sync_id": "3kdlVcMVsSkbsUy8eQcBjI",
					"state": "Ark Patrol; Veronika Redd",
					"details": "Let Go",
					"assets": {
						"large_image": "spotify:ab67616d0000b27364840995fe43bb2ec73a241d",
						"large_text": "Let Go"
					},
					"timestamps": {"start": 1615529820677, "end": 1615530068733}
				}
			]
		}`)

		p := BuildPretty(user, presenceRaw, nil)
		if !p.ListeningToSpotify || p.Spotify == nil {
			t.Fatalf("expected listening to spotify")
		}
		if *p.Spotify.TrackID != "3kdlVcMVsSkbsUy8eQcBjI" {
			t.Errorf("track_id mismatch: %v", *p.Spotify.TrackID)
		}
		if *p.Spotify.AlbumArtURL != "https://i.scdn.co/image/ab67616d0000b27364840995fe43bb2ec73a241d" {
			t.Errorf("album_art_url mismatch: %v", *p.Spotify.AlbumArtURL)
		}
		if *p.Spotify.Album != "Let Go" {
			t.Errorf("album mismatch: %v", *p.Spotify.Album)
		}

		// Verify assets as string case
		presenceRaw2 := decodeJSON(t, `{
			"status": "online",
			"activities": [
				{
					"id": "spotify:1",
					"sync_id": null,
					"state": "Artist",
					"details": "Song",
					"assets": "Simple Album String"
				}
			]
		}`)
		p2 := BuildPretty(user, presenceRaw2, nil)
		if p2.Spotify.TrackID != nil {
			t.Errorf("expected null track_id when sync_id is null")
		}
		if *p2.Spotify.Album != "Simple Album String" {
			t.Errorf("expected album string, got %v", *p2.Spotify.Album)
		}
		if p2.Spotify.AlbumArtURL != nil {
			t.Errorf("expected nil album_art_url")
		}
	})

	t.Run("fixture (d): numeric application_id and emoji.id become strings", func(t *testing.T) {
		user := decodeJSON(t, `{"id": "123", "username": "dave"}`)
		presenceRaw := decodeJSON(t, `{
			"status": "online",
			"activities": [
				{
					"name": "Game",
					"application_id": 987654321012345678,
					"emoji": {
						"name": "cool",
						"id": 112233445566778899
					}
				}
			]
		}`)

		p := BuildPretty(user, presenceRaw, nil)
		if len(p.Activities) != 1 {
			t.Fatalf("expected 1 activity")
		}
		act := p.Activities[0]
		if act["application_id"] != "987654321012345678" {
			t.Errorf("expected string application_id, got %v (%T)", act["application_id"], act["application_id"])
		}
		emoji := act["emoji"].(map[string]any)
		if emoji["id"] != "112233445566778899" {
			t.Errorf("expected string emoji.id, got %v (%T)", emoji["id"], emoji["id"])
		}
	})

	t.Run("fixture (e): missing client_status", func(t *testing.T) {
		user := decodeJSON(t, `{"id": "123", "username": "eve"}`)
		presenceRaw := decodeJSON(t, `{
			"status": "idle"
		}`)

		p := BuildPretty(user, presenceRaw, nil)
		if p.DiscordStatus != "idle" {
			t.Errorf("expected idle, got %s", p.DiscordStatus)
		}
		if p.ActiveOnDiscordDesktop || p.ActiveOnDiscordWeb || p.ActiveOnDiscordMobile {
			t.Errorf("expected all active flags false")
		}
	})

	t.Run("fixture (f): huge snowflake stays exact", func(t *testing.T) {
		snowflake := "1234567890123456789"
		user := decodeJSON(t, `{"id": 1234567890123456789, "username": "frank"}`)

		p := BuildPretty(user, nil, nil)
		if p.DiscordUser["id"] != snowflake {
			t.Errorf("expected %s, got %v", snowflake, p.DiscordUser["id"])
		}
	})
}
