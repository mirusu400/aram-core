package guest

func CloneMap[K comparable, V any](source map[K]V) map[K]V {
	result := make(map[K]V, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func CloneSliceMap[K comparable, E any](source map[K][]E) map[K][]E {
	result := make(map[K][]E, len(source))
	for key, value := range source {
		result[key] = append([]E(nil), value...)
	}
	return result
}

func CloneByteSlices(source [][]byte) [][]byte {
	result := make([][]byte, len(source))
	for index, value := range source {
		result[index] = append([]byte(nil), value...)
	}
	return result
}
