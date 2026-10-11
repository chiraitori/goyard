package presence

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Spotify represents the structured Spotify activity in pretty presence
type Spotify struct {
	TrackID     *string `json:"track_id"`
	Artist      *string `json:"artist"`
	Song        *string `json:"song"`
	Album       *string `json:"album"`
	AlbumArtURL *string `json:"album_art_url"`
	Timestamps  any     `json:"timestamps"`
}

// PrettyPresence represents the public JSON presence shape returned by Lanyard
type PrettyPresence struct {
	DiscordUser             map[string]any    `json:"discord_user"`
	DiscordStatus           string            `json:"discord_status"`
	ActiveOnDiscordWeb      bool              `json:"active_on_discord_web"`
	ActiveOnDiscordDesktop  bool              `json:"active_on_discord_desktop"`
	ActiveOnDiscordMobile   bool              `json:"active_on_discord_mobile"`
	ActiveOnDiscordEmbedded bool              `json:"active_on_discord_embedded"`
	ActiveOnDiscordVR       bool              `json:"active_on_discord_vr"`
	ListeningToSpotify      bool              `json:"listening_to_spotify"`
	Spotify                 *Spotify          `json:"spotify"`
	Activities              []map[string]any  `json:"activities"`
	KV                      map[string]string `json:"kv"`
}

// BuildPretty converts raw user and presence objects into PrettyPresence
func BuildPretty(discordUser map[string]any, discordPresence map[string]any, kv map[string]string) *PrettyPresence {
	// Deep copy or clone user map ensuring ID is string if numeric
	userCopy := make(map[string]any, len(discordUser))
	for k, v := range discordUser {
		userCopy[k] = v
	}
	if idVal, ok := userCopy["id"]; ok {
		switch id := idVal.(type) {
		case json.Number:
			userCopy["id"] = id.String()
		case float64:
			userCopy["id"] = fmt.Sprintf("%.0f", id)
		case int64:
			userCopy["id"] = fmt.Sprintf("%d", id)
		case int:
			userCopy["id"] = fmt.Sprintf("%d", id)
		}
	}

	kvCopy := make(map[string]string)
	if kv != nil {
		for k, v := range kv {
			kvCopy[k] = v
		}
	}

	if discordPresence == nil {
		return &PrettyPresence{
			DiscordUser:             userCopy,
			DiscordStatus:           "offline",
			ActiveOnDiscordWeb:      false,
			ActiveOnDiscordDesktop:  false,
			ActiveOnDiscordMobile:   false,
			ActiveOnDiscordEmbedded: false,
			ActiveOnDiscordVR:       false,
			ListeningToSpotify:      false,
			Spotify:                 nil,
			Activities:              make([]map[string]any, 0),
			KV:                      kvCopy,
		}
	}

	status := "offline"
	if s, ok := discordPresence["status"].(string); ok {
		status = s
	}

	var web, desktop, mobile, embedded, vr bool
	if cs, ok := discordPresence["client_status"].(map[string]any); ok {
		_, web = cs["web"]
		_, desktop = cs["desktop"]
		_, mobile = cs["mobile"]
		_, embedded = cs["embedded"]
		_, vr = cs["vr"]
	}

	var rawActivities []any
	if acts, ok := discordPresence["activities"].([]any); ok {
		rawActivities = acts
	}

	var spotifyActivity map[string]any
	decoratedActivities := make([]map[string]any, 0, len(rawActivities))

	for _, item := range rawActivities {
		act, ok := item.(map[string]any)
		if !ok {
			continue
		}
		// Spotify activity is the first one where id == "spotify:1"
		if spotifyActivity == nil {
			if id, ok := act["id"].(string); ok && id == "spotify:1" {
				spotifyActivity = act
			}
		}

		// Decorate activity
		actCopy := make(map[string]any, len(act))
		for k, v := range act {
			actCopy[k] = v
		}

		// If application_id is a JSON number (or numeric type) -> convert to string
		if appID, ok := actCopy["application_id"]; ok {
			switch val := appID.(type) {
			case json.Number:
				actCopy["application_id"] = val.String()
			case float64:
				actCopy["application_id"] = fmt.Sprintf("%.0f", val)
			case int64:
				actCopy["application_id"] = fmt.Sprintf("%d", val)
			case int:
				actCopy["application_id"] = fmt.Sprintf("%d", val)
			}
		}

		// If emoji is an object whose id is a JSON number -> convert that id to a string
		if emojiVal, ok := actCopy["emoji"].(map[string]any); ok {
			emojiCopy := make(map[string]any, len(emojiVal))
			for ek, ev := range emojiVal {
				emojiCopy[ek] = ev
			}
			if eid, ok := emojiCopy["id"]; ok {
				switch val := eid.(type) {
				case json.Number:
					emojiCopy["id"] = val.String()
				case float64:
					emojiCopy["id"] = fmt.Sprintf("%.0f", val)
				case int64:
					emojiCopy["id"] = fmt.Sprintf("%d", val)
				case int:
					emojiCopy["id"] = fmt.Sprintf("%d", val)
				}
			}
			actCopy["emoji"] = emojiCopy
		}

		decoratedActivities = append(decoratedActivities, actCopy)
	}

	var prettySpotify *Spotify
	if spotifyActivity != nil {
		prettySpotify = buildPrettySpotify(spotifyActivity)
	}

	return &PrettyPresence{
		DiscordUser:             userCopy,
		DiscordStatus:           status,
		ActiveOnDiscordWeb:      web,
		ActiveOnDiscordDesktop:  desktop,
		ActiveOnDiscordMobile:   mobile,
		ActiveOnDiscordEmbedded: embedded,
		ActiveOnDiscordVR:       vr,
		ListeningToSpotify:      spotifyActivity != nil,
		Spotify:                 prettySpotify,
		Activities:              decoratedActivities,
		KV:                      kvCopy,
	}
}

func buildPrettySpotify(act map[string]any) *Spotify {
	var trackID *string
	if syncID, ok := act["sync_id"].(string); ok {
		trackID = &syncID
	}

	var artist *string
	if st, ok := act["state"].(string); ok {
		artist = &st
	}

	var song *string
	if det, ok := act["details"].(string); ok {
		song = &det
	}

	var album *string
	var albumArtURL *string

	if assetsVal, ok := act["assets"]; ok {
		switch assets := assetsVal.(type) {
		case map[string]any:
			if lt, ok := assets["large_text"].(string); ok {
				album = &lt
			}
			if li, ok := assets["large_image"].(string); ok {
				parts := strings.Split(li, ":")
				if len(parts) == 2 {
					u := fmt.Sprintf("https://i.scdn.co/image/%s", parts[1])
					albumArtURL = &u
				}
			}
		case string:
			album = &assets
		}
	}

	timestamps := act["timestamps"]

	return &Spotify{
		TrackID:     trackID,
		Artist:      artist,
		Song:        song,
		Album:       album,
		AlbumArtURL: albumArtURL,
		Timestamps:  timestamps,
	}
}
