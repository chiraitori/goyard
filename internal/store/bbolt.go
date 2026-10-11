package store

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"
)

var (
	bucketKV       = []byte("kv")
	bucketAPIKeys  = []byte("api_keys")
	bucketUserKeys = []byte("user_keys")
)

type BboltStore struct {
	db *bolt.DB
}

func NewBboltStore(path string) (*BboltStore, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create directory for bbolt: %w", err)
	}

	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, fmt.Errorf("failed to open bbolt db: %w", err)
	}

	err = db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists(bucketKV); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists(bucketAPIKeys); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists(bucketUserKeys); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to init bbolt buckets: %w", err)
	}

	return &BboltStore{db: db}, nil
}

func (s *BboltStore) GetKV(ctx context.Context, userID string) (map[string]string, error) {
	res := make(map[string]string)
	err := s.db.View(func(tx *bolt.Tx) error {
		kvBucket := tx.Bucket(bucketKV)
		if kvBucket == nil {
			return nil
		}
		userBucket := kvBucket.Bucket([]byte(userID))
		if userBucket == nil {
			return nil
		}
		return userBucket.ForEach(func(k, v []byte) error {
			res[string(k)] = string(v)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (s *BboltStore) SetKV(ctx context.Context, userID, key, value string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		kvBucket := tx.Bucket(bucketKV)
		userBucket, err := kvBucket.CreateBucketIfNotExists([]byte(userID))
		if err != nil {
			return err
		}

		// 1. Key-count limit using merged key set
		exists := userBucket.Get([]byte(key)) != nil
		if !exists && userBucket.Stats().KeyN >= 512 {
			return ErrSingleLimit
		}

		// 2. Validate key/value
		if err := ValidateKey(key); err != nil {
			return err
		}
		if err := ValidateValue(value); err != nil {
			return err
		}

		return userBucket.Put([]byte(key), []byte(value))
	})
}

func (s *BboltStore) MultiSetKV(ctx context.Context, userID string, pairs map[string]string) error {
	// 1. Validate every pair first
	for k, v := range pairs {
		if err := ValidateKey(k); err != nil {
			return err
		}
		if err := ValidateValue(v); err != nil {
			return err
		}
	}

	return s.db.Update(func(tx *bolt.Tx) error {
		kvBucket := tx.Bucket(bucketKV)
		userBucket, err := kvBucket.CreateBucketIfNotExists([]byte(userID))
		if err != nil {
			return err
		}

		// 2. Key-count limit on the merged set
		mergedCount := userBucket.Stats().KeyN
		for k := range pairs {
			if userBucket.Get([]byte(k)) == nil {
				mergedCount++
			}
		}
		if mergedCount > 512 {
			return ErrMultiLimit
		}

		// 3. Write all
		for k, v := range pairs {
			if err := userBucket.Put([]byte(k), []byte(v)); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *BboltStore) DelKV(ctx context.Context, userID, key string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		kvBucket := tx.Bucket(bucketKV)
		userBucket := kvBucket.Bucket([]byte(userID))
		if userBucket == nil {
			return nil
		}
		return userBucket.Delete([]byte(key))
	})
}

func (s *BboltStore) UserIDForKey(ctx context.Context, key string) (string, bool, error) {
	var uid string
	var found bool
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketAPIKeys)
		if b == nil {
			return nil
		}
		v := b.Get([]byte(key))
		if v != nil {
			uid = string(v)
			found = true
		}
		return nil
	})
	return uid, found, err
}

func (s *BboltStore) KeyForUser(ctx context.Context, userID string) (string, bool, error) {
	var key string
	var found bool
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketUserKeys)
		if b == nil {
			return nil
		}
		v := b.Get([]byte(userID))
		if v != nil {
			key = string(v)
			found = true
		}
		return nil
	})
	return key, found, err
}

func (s *BboltStore) RotateKey(ctx context.Context, userID string) (string, error) {
	newKey, err := GenerateAPIKey()
	if err != nil {
		return "", err
	}

	err = s.db.Update(func(tx *bolt.Tx) error {
		apiKeys := tx.Bucket(bucketAPIKeys)
		userKeys := tx.Bucket(bucketUserKeys)

		if oldKey := userKeys.Get([]byte(userID)); oldKey != nil {
			if err := apiKeys.Delete(oldKey); err != nil {
				return err
			}
		}

		if err := userKeys.Put([]byte(userID), []byte(newKey)); err != nil {
			return err
		}
		return apiKeys.Put([]byte(newKey), []byte(userID))
	})
	if err != nil {
		return "", err
	}
	return newKey, nil
}

func (s *BboltStore) Backup(w io.Writer) error {
	return s.db.View(func(tx *bolt.Tx) error {
		_, err := tx.WriteTo(w)
		return err
	})
}

func (s *BboltStore) Close() error {
	return s.db.Close()
}
