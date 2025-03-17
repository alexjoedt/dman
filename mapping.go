package dman

type DotfileMapping struct {
	mapping map[string]string
	errors  []error
}

func (dm *DotfileMapping) Set(src string, dst string) {
	dm.mapping[src] = dst
}

func (dm *DotfileMapping) Apply() error {
	for d, o := range dm.mapping {
		err := copyFile(o, d)
		if err != nil {
			dm.errors = append(dm.errors, err)
		}
	}

	return nil
}

func (dm *DotfileMapping) GetFilesFromHome() []string {
	homeFiles := make([]string, 0, len(dm.mapping))
	for _, hf := range dm.mapping {
		homeFiles = append(homeFiles, hf)
	}
	return homeFiles

}

func (dm *DotfileMapping) Len() int {
	return len(dm.mapping)
}
