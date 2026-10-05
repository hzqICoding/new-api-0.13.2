package model

// CodexSetupGroupModels reads local enabled channel abilities only. It neither
// contacts a provider nor changes token access times, balances or cached quotas.
func CodexSetupGroupModels(groups []string) ([]string, error) {
	models := []string{}
	if len(groups) == 0 {
		return models, nil
	}
	err := DB.Model(&Ability{}).Where(map[string]interface{}{"group": groups, "enabled": true}).
		Distinct("model").Pluck("model", &models).Error
	return models, err
}
