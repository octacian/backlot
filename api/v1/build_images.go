package v1

import (
	"encoding/hex"
	"regexp"
	"strings"
)

var buildImageIdentifier = regexp.MustCompile(schemaIdentifier)

func buildImageHex(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && len(value) == 64 && strings.ToLower(value) == value
}

func buildImageID(value string) bool {
	digest, ok := strings.CutPrefix(value, "sha256:")
	return ok && buildImageHex(digest)
}

func invalidBuildImage() error {
	return &PlanError{Code: "invalid_response", Message: "invalid build-image provenance or compatibility warning"}
}

// Validate checks bounded provenance and binds its tag to the instance and source attempt.
// Retained source attempts may precede the execution currently reporting the image.
func (r BuildImageResult) Validate(instanceID string) error {
	if !buildImageHex(instanceID) || !buildImageIdentifier.MatchString(r.Name) || !buildImageIdentifier.MatchString(r.Producer) || !buildImageHex(r.Attempt) || !buildImageID(r.ID) || r.Tag != "backlot-build/"+instanceID+":"+r.Attempt+"-"+r.Name {
		return invalidBuildImage()
	}
	return nil
}

// Validate checks a bounded compatibility warning without implying execution authority.
func (r ImageChangeWarning) Validate() error {
	if !buildImageIdentifier.MatchString(r.Component) || !buildImageIdentifier.MatchString(r.Image) || !buildImageID(r.PreviousID) || !buildImageID(r.ID) || r.PreviousID == r.ID || strings.TrimSpace(r.Message) == "" || len(r.Message) > 256 || strings.ContainsAny(r.Message, "\x00\r\n") {
		return invalidBuildImage()
	}
	return nil
}

// ValidateBuildImages checks unique image/consumer identities and warning-to-image links.
// Optional absence is valid, and warnings may precede consumer launch or survive failure.
func ValidateBuildImages(instanceID string, images []BuildImageResult, warnings []ImageChangeWarning) error {
	accepted := make(map[string]BuildImageResult, len(images))
	for _, image := range images {
		if _, exists := accepted[image.Name]; exists || image.Validate(instanceID) != nil {
			return invalidBuildImage()
		}
		accepted[image.Name] = image
	}
	seen := make(map[string]bool, len(warnings))
	for _, warning := range warnings {
		image, exists := accepted[warning.Image]
		if warning.Validate() != nil || seen[warning.Component] || !exists || warning.ID != image.ID {
			return invalidBuildImage()
		}
		seen[warning.Component] = true
	}
	return nil
}
