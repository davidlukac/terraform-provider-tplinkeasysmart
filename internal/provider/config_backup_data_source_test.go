package provider

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func backupFake() (*fakeClient, []byte) {
	f := &fakeClient{}
	data := append([]byte{0x23, 0x89, 0x23, 0x89}, []byte("config-bytes")...)
	f.extra().backup = data
	return f, data
}

func TestAccConfigBackupDefaultsToSizeAndHash(t *testing.T) {
	f, data := backupFake()
	sum := sha256.Sum256(data)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{{
			Config: providerBlock + `data "tplinkeasysmart_config_backup" "b" {}`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.tplinkeasysmart_config_backup.b", "size", "16"),
				resource.TestCheckResourceAttr("data.tplinkeasysmart_config_backup.b", "sha256", hex.EncodeToString(sum[:])),
				resource.TestCheckNoResourceAttr("data.tplinkeasysmart_config_backup.b", "content_base64"),
			),
		}},
	})
}

func TestAccConfigBackupIncludesContentOnRequest(t *testing.T) {
	f, data := backupFake()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{{
			Config: providerBlock + "data \"tplinkeasysmart_config_backup\" \"b\" {\n  include_content = true\n}\n",
			Check:  resource.TestCheckResourceAttr("data.tplinkeasysmart_config_backup.b", "content_base64", base64.StdEncoding.EncodeToString(data)),
		}},
	})
}

func TestAccConfigBackupReadError(t *testing.T) {
	f, _ := backupFake()
	f.extra().backupErr = errBoom
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{{
			Config:      providerBlock + `data "tplinkeasysmart_config_backup" "b" {}`,
			ExpectError: regexp.MustCompile(`Reading the configuration backup failed`),
		}},
	})
}
