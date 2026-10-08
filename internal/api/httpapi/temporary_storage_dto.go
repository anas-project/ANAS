package httpapi

import "github.com/anas-project/ANAS/internal/application"

func normalizeTempSwitch(in *application.TempSwitchPlan) *application.TempSwitchPlan {
	if in == nil {
		return nil
	}
	out := *in
	out.StopModules = nonNilStrings(in.StopModules)
	out.StartModules = nonNilStrings(in.StartModules)
	return &out
}

func normalizeModuleTemporaryStorage(in *application.ModuleTemporaryStorageStatus) *application.ModuleTemporaryStorageStatus {
	if in == nil {
		return nil
	}
	out := *in
	out.Issues = append([]application.ModuleTemporaryStorageIssue{}, in.Issues...)
	return &out
}
