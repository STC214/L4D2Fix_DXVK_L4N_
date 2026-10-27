//go:build windows

package main

import (
	"fmt"
	"sort"
)

// Read only local records and payloads. No HTTP request, subscription change,
// or deployment is performed when opening the application.
func selectDeployablePlatformResource(res, kind, version string) (workshopResource, string, error) {
	idx, err := readWorkshopIndex(res)
	if err != nil {
		return workshopResource{}, "", err
	}
	var candidates []workshopResource
	for _, r := range idx.Resources {
		if r.Platform == kind && (version == "" || r.Version == version) {
			candidates = append(candidates, r)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i].Archive == idx.Latest[kind], candidates[j].Archive == idx.Latest[kind]
		if a != b {
			return a
		}
		return candidates[i].Detail.Updated > candidates[j].Detail.Updated
	})
	for _, r := range candidates {
		p, err := verifiedWorkshopArchive(res, r)
		if err != nil {
			continue
		}
		if kind == "skeeto" {
			if _, err := preparedSkeetoMods(res, r.Detail.Content); err != nil {
				continue
			}
		}
		return r, p, nil
	}
	return workshopResource{}, "", fmt.Errorf("缺少完整的本地 %s 资源；可放入随包资源，或联网检查更新后补充", kind)
}

func localPlatformResources(res string) map[string]workshopResource {
	ready := map[string]workshopResource{}
	for _, kind := range []string{"evolved", "skeeto"} {
		if r, _, err := selectDeployablePlatformResource(res, kind, ""); err == nil {
			ready[kind] = r
		}
	}
	return ready
}

func loadLocalResourceChoices() {
	loadDxvkOptionsIntoCombo()
	loadL4nOptionsIntoCombo()
	if root, err := packageRoot(); err == nil {
		ready := localPlatformResources(resourceRoot(root))
		platformReadyMu.Lock()
		platformReady = ready
		platformReadyMu.Unlock()
	}
	refreshPlatformChoices()
}
