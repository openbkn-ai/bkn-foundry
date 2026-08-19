package utils

import (
	jsoniter "github.com/json-iterator/go"
)

// ObjectToJSON converts objects to JSON strings.
func ObjectToJSON(obj interface{}) string {
	jsonBytes, _ := jsoniter.Marshal(obj)
	return string(jsonBytes)
}

// ObjectToByte converts an object to a byte array.
func ObjectToByte(obj interface{}) []byte {
	jsonBytes, _ := jsoniter.Marshal(obj)
	return jsonBytes
}

// SubtractStrings returns the difference set that does not contain list2 elements in list1 (retains the original order of list1)
func SubtractStrings(list1, list2 []string) []string {
	excludeSet := make(map[string]struct{})
	for _, s := range list2 {
		excludeSet[s] = struct{}{}
	}
	// Filter to keep elements not in the excluded set.
	var result []string
	for _, s := range list1 {
		if _, exists := excludeSet[s]; !exists {
			result = append(result, s)
		}
	}
	return result
}

// UniqueStrings deduplication function.
func UniqueStrings(input []string) []string {
	// Create a map to store unique strings.
	uniqueMap := make(map[string]struct{})
	var uniqueList []string

	for _, str := range input {
		if _, exists := uniqueMap[str]; !exists {
			if str == "" {
				continue
			}
			uniqueMap[str] = struct{}{}          // Mark a string as existing.
			uniqueList = append(uniqueList, str) // Add to results list.
		}
	}

	return uniqueList
}

// StringToObject converts JSON string to object.
func StringToObject(jsonStr string, obj interface{}) error {
	err := jsoniter.Unmarshal([]byte(jsonStr), obj)
	return err
}

// CompareStringSliceLists compares two string slice lists and returns different elements.
func CompareStringSliceLists(list1, list2 []string) []string {
	var result []string
	// Create a map to store the elements in list1.
	map1 := make(map[string]bool)
	for _, item := range list1 {
		map1[item] = true
	}
	// Check if the element in list2 exists in map1.
	for _, item := range list2 {
		if !map1[item] {
			result = append(result, item)
		}
	}
	return result
}

// FindMissingElements finds the missing list1 elements in list2 (assuming list2 is a subset of list1)
func FindMissingElements(list1, list2 []string) []string {
	// Create an existence mapping table.
	present := make(map[string]bool)
	for _, item := range list2 {
		present[item] = true
	}

	// Check and collect missing elements.
	var missing []string
	seen := make(map[string]bool) // Used to deduplicate results.
	for _, item := range list1 {
		if !present[item] && !seen[item] {
			missing = append(missing, item)
			seen[item] = true
		}
	}
	return missing
}

// GetDuplicateStrings gets the duplicate elements in the string list and returns.
func GetDuplicateStrings(strings []string) []string {
	seen := make(map[string]bool)
	duplicates := make(map[string]bool)
	for _, str := range strings {
		if seen[str] {
			duplicates[str] = true
		} else {
			seen[str] = true
		}
	}
	var result []string
	for str := range duplicates {
		result = append(result, str)
	}
	return result
}
