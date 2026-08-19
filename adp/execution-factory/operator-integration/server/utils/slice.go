package utils

// SliceToInterface converts a slice of any type into an interface slice.
func SliceToInterface[T any](slice []T) []interface{} {
	interfaces := make([]interface{}, len(slice))
	for i, item := range slice {
		interfaces[i] = item
	}
	return interfaces
}

// RemoveStringFromSlice removes an element from the string list.
func RemoveStringFromSlice(strings []string, target string) []string {
	var result []string
	for _, str := range strings {
		if str != target {
			result = append(result, str)
		}
	}
	return result
}

// CalculateIntersection calculates the intersection of two string slices to optimize performance.
func CalculateIntersection(list1, list2 []string) []string {
	// If any list is empty, return an empty list directly.
	if len(list1) == 0 || len(list2) == 0 {
		return []string{}
	}

	// Optimization: use smaller lists to build maps, reducing memory usage.
	if len(list1) > len(list2) {
		list1, list2 = list2, list1
	}

	// Build a map of a smaller list.
	mapping := make(map[string]struct{}, len(list1))
	for _, id := range list1 {
		mapping[id] = struct{}{}
	}

	// Preallocate result slices, up to the possible length of the smaller list.
	result := make([]string, 0, len(list1))
	for _, id := range list2 {
		if _, exists := mapping[id]; exists {
			result = append(result, id)
		}
	}

	return result
}
