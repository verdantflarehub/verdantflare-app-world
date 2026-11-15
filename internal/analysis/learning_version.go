package analysis

// Empty is the original task's omitted JSON field, preserving old fingerprints.
func NormalizeAssemblyVersion(version string) (string, error) {
	switch version {
	case "", "1":
		return "", nil
	case "2":
		return "2", nil
	default:
		return "", ErrInvalid
	}
}

func (t Task) LearningAssemblyVersion() string {
	if t.AssemblyVersion == "" {
		return "1"
	}
	return t.AssemblyVersion
}
