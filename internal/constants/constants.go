package constants

import "github.com/bl4ko/netbox-ssot/internal/netbox/objects"

type SourceType string

const (
	Ovirt  SourceType = "ovirt"
	Vmware SourceType = "vmware"
	Dnac   SourceType = "dnac"
)

// Default mappings of sources to colors (for tags)
var DefaultSourceToTagColorMap = map[SourceType]string{
	Ovirt:  objects.COLOR_DARK_RED,
	Vmware: objects.COLOR_LIGHT_GREEN,
	Dnac:   objects.COLOR_LIGHT_BLUE,
}

// Object for mapping source type to tag color
var SourceTypeToTagColorMap = map[SourceType]string{
	Ovirt:  objects.COLOR_RED,
	Vmware: objects.COLOR_GREEN,
	Dnac:   objects.COLOR_BLUE,
}