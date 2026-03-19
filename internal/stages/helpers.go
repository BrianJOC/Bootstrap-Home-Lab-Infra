package stages

import "strconv"

// intToStr converts an int to its string representation for Ansible extra vars.
func intToStr(i int) string {
	return strconv.Itoa(i)
}
