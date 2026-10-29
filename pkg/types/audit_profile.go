// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package types

import (
	"fmt"
	"net/mail"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

func (a AuditAttribution) validateProfiles() error {
	if err := validateAuditActorProfile(a.ActorRef, a.ActorProfile); err != nil {
		return fmt.Errorf("audit.attribution.actor_profile: %w", err)
	}
	if err := validateAuditActorProfile(a.EffectiveActorRef, a.EffectiveActorProfile); err != nil {
		return fmt.Errorf("audit.attribution.effective_actor_profile: %w", err)
	}
	if a.ActorRef == a.EffectiveActorRef && !sameAuditProfile(a.ActorProfile, a.EffectiveActorProfile) {
		return fmt.Errorf("matching actor references require matching profile snapshots")
	}
	return nil
}

func validateAuditActorProfile(ref string, profile *AuditActorProfile) error {
	if strings.HasPrefix(ref, "service:") {
		if profile != nil {
			return fmt.Errorf("service actors must not carry a user profile")
		}
		return nil
	}
	if profile == nil {
		return fmt.Errorf("user actors require a profile snapshot or explicit unavailable status")
	}
	values := []struct {
		name  string
		value *string
		limit int
	}{
		{"name", profile.Name, 1024},
		{"avatar_url", profile.AvatarURL, 2048},
		{"email", profile.Email, 320},
		{"username", profile.Username, 256},
	}
	present := 0
	for _, field := range values {
		if field.value == nil {
			continue
		}
		present++
		if !auditProfileText(*field.value, field.limit) {
			return fmt.Errorf("invalid %s attribute; use null if unavailable", field.name)
		}
	}
	switch profile.Availability {
	case AuditProfileCaptured:
		if present == 0 {
			return fmt.Errorf("captured profiles require at least one available attribute")
		}
	case AuditProfileUnavailable:
		if present != 0 {
			return fmt.Errorf("unavailable profiles require all attributes to be null")
		}
	default:
		return fmt.Errorf("invalid profile availability")
	}
	if profile.Email != nil {
		address, err := mail.ParseAddress(*profile.Email)
		if err != nil || address.Name != "" || strings.TrimSpace(*profile.Email) != *profile.Email {
			return fmt.Errorf("email must be a bare mailbox address")
		}
		// ParseAddress alone also accepts <addr> and an empty display-name
		// wrapper. An enclosing name-addr forces the supplied value to parse as
		// addr-spec, while preserving valid quoted local parts verbatim.
		if _, err := mail.ParseAddress("audit <" + *profile.Email + ">"); err != nil {
			return fmt.Errorf("email must be a bare mailbox address")
		}
	}
	if profile.AvatarURL != nil {
		avatar, err := url.Parse(*profile.AvatarURL)
		if err != nil || !strings.EqualFold(avatar.Scheme, "https") || avatar.Hostname() == "" ||
			avatar.User != nil || avatar.RawQuery != "" || avatar.ForceQuery || strings.Contains(*profile.AvatarURL, "#") {
			return fmt.Errorf("avatar_url must be an absolute HTTPS URL without userinfo, query, or fragment")
		}
	}
	return nil
}

func auditProfileText(value string, limit int) bool {
	if len(value) > limit || strings.TrimSpace(value) == "" || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func sameAuditProfile(a, b *AuditActorProfile) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Availability == b.Availability && sameAuditAttribute(a.Name, b.Name) &&
		sameAuditAttribute(a.AvatarURL, b.AvatarURL) && sameAuditAttribute(a.Email, b.Email) &&
		sameAuditAttribute(a.Username, b.Username)
}

func sameAuditAttribute(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
