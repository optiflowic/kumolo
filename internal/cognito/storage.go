package cognito

// Storage persists Cognito user pool data under dataDir.
// Methods are added incrementally as operations are implemented.
type Storage struct {
	dataDir string
}

func NewStorage(dataDir string) (*Storage, error) {
	return &Storage{dataDir: dataDir}, nil
}

func (s *Storage) Close() error {
	return nil
}
