package utils

// GetValueOrDefault gets the value corresponding to the key in the map. If it does not exist, it returns the default value.
func GetValueOrDefault(m map[string]string, key, defaultValue string) string {
	if key == "" {
		return defaultValue
	}
	if value, exists := m[key]; exists {
		return value
	}
	if defaultValue == "" {
		return key
	}
	return defaultValue
}
