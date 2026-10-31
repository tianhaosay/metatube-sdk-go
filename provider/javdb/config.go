package javdb

import (
	"github.com/metatube-community/metatube-sdk-go/common/throttle"
	"github.com/metatube-community/metatube-sdk-go/provider"
)

// SetConfig implements provider.ConfigSetter.
//
// JavDB's only remaining knob is its request cooldown. Actor/genre mapping and
// translation used to live here; they are now engine-level post-processing
// (see common/enrich) so that they apply to every provider, not just this one.
//
// Keys (env: MT_PROVIDER_JAVDB__<KEY>):
//
//	rate_limit - minimum interval between two javdb requests (default 10s;
//	             <=0 disables). Cloudflare throttles this domain hard.
//
// JavDB ships disabled (Priority 0). To try it as a long-tail fallback:
//
//	MT_PROVIDER_JAVDB__PRIORITY=900
func (db *JavDB) SetConfig(config provider.Config) error {
	if config == nil {
		return nil
	}
	if config.Has("rate_limit") {
		if v, err := config.GetDuration("rate_limit"); err == nil {
			throttle.Set(Name, v)
		}
	}
	return nil
}
