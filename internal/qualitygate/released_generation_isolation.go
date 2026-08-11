package main

type releasedGenerationIsolation struct {
	GOWorkOff                 bool `json:"gowork_off"`
	FreshModuleCaches         bool `json:"fresh_module_caches"`
	NoReplace                 bool `json:"no_replace"`
	PublicProxy               bool `json:"public_proxy"`
	PublicSumDB               bool `json:"public_sumdb"`
	PeerVendorOfflineBuild    bool `json:"peer_vendor_offline_build"`
	FixtureModuleCacheOffline bool `json:"fixture_module_cache_offline_build"`
}
