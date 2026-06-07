package generated

// K8sPresetItem is the hand-defined item type for the `/api/v1/presets/k8s`
// response list. Upstream models that list as a discriminated oneOf
// (master|worker), which oapi-codegen emits as the invalid Go identifier
// `200_K8sPresets_Item`; `make generate-client` sed-renames that reference to
// this type. Master and worker presets share an identical field set (worker
// omits `limit`), so a single flat struct covers both — the resolver's K8s
// preset fetchers filter on `Type` ("master" / "worker").
type K8sPresetItem struct {
	Id               *int     `json:"id,omitempty"`
	Type             *string  `json:"type,omitempty"`
	Description      *string  `json:"description,omitempty"`
	DescriptionShort *string  `json:"description_short,omitempty"`
	Price            *float64 `json:"price,omitempty"`
	Cpu              *int     `json:"cpu,omitempty"`
	Ram              *int     `json:"ram,omitempty"`
	Disk             *int     `json:"disk,omitempty"`
	Network          *int     `json:"network,omitempty"`
	Limit            *int     `json:"limit,omitempty"`
}
