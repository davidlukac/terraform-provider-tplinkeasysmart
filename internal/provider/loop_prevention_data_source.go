package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
)

// loopPreventionReader is the part of the switch API the loop prevention data source reads.
type loopPreventionReader interface {
	LoopPrevention(ctx context.Context) (bool, error)
}

// NewLoopPreventionDataSource returns the tplinkeasysmart_loop_prevention data source.
func NewLoopPreventionDataSource() datasource.DataSource {
	return &boolSettingDataSource[loopPreventionReader]{spec: boolSettingSpec[loopPreventionReader]{
		suffix:      "loop_prevention",
		id:          loopPreventionID,
		description: "Whether loop prevention is enabled on the switch (Monitoring > Loop Prevention).",
		enabledDesc: "Whether loop prevention is enabled.",
		readFailure: "Reading loop prevention failed",
		readSetting: func(ctx context.Context, c loopPreventionReader) (bool, error) { return c.LoopPrevention(ctx) },
	}}
}
