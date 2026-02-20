package exports

import "strconv"

func toString(v int64) string {
	return strconv.FormatInt(v, 10)
}
