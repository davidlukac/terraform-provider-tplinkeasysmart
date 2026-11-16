package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
)

// ledReader is the part of the switch API the LED data source reads.
type ledReader interface {
	LED(ctx context.Context) (bool, error)
}

// NewLEDDataSource returns the tplinkeasysmart_led data source.
func NewLEDDataSource() datasource.DataSource {
	return &boolSettingDataSource[ledReader]{spec: boolSettingSpec[ledReader]{
		suffix:      "led",
		id:          ledID,
		description: "Whether the front-panel LEDs of the switch are on (System > LED On/Off in the web UI).",
		enabledDesc: "Whether the port LEDs are on.",
		readFailure: "Reading LED failed",
		readSetting: func(ctx context.Context, c ledReader) (bool, error) { return c.LED(ctx) },
	}}
}
