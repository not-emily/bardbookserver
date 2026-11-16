package space

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrNameTaken     = errors.New("space: a collection of that name already exists")
	ErrAlreadyInside = errors.New("space: that book is already in the collection")
	ErrNameRequired  = errors.New("space: a collection needs a name")
)

// CreateCollection makes one owned by userID. Curated collections are made by
// an admin through the same call with a nil owner.
func (s *Store) CreateCollection(ctx context.Context, ownerID *int64, name, description string) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, ErrNameRequired
	}

	var text any
	if description = strings.TrimSpace(description); description != "" {
		text = description
	}

	res, err := s.db.ExecContext(ctx,
		`INSERT INTO collections (owner_id, name, description) VALUES (?, ?, ?)`,
		ownerID, name, text)
	if err != nil {
		if isUniqueViolation(err) {
			return 0, ErrNameTaken
		}
		return 0, fmt.Errorf("create collection: %w", err)
	}
	return res.LastInsertId()
}

// RenameCollection leaves a field alone when its pointer is nil, so a client
// can change the name without resending the description.
func (s *Store) RenameCollection(ctx context.Context, id int64, name, description *string) error {
	assignments := make([]string, 0, 2)
	args := make([]any, 0, 3)

	if name != nil {
		trimmed := strings.TrimSpace(*name)
		if trimmed == "" {
			return ErrNameRequired
		}
		assignments = append(assignments, "name = ?")
		args = append(args, trimmed)
	}
	if description != nil {
		var text any
		if trimmed := strings.TrimSpace(*description); trimmed != "" {
			text = trimmed
		}
		assignments = append(assignments, "description = ?")
		args = append(args, text)
	}
	if len(assignments) == 0 {
		return nil
	}

	args = append(args, id)
	_, err := s.db.ExecContext(ctx,
		`UPDATE collections SET `+strings.Join(assignments, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrNameTaken
		}
		return fmt.Errorf("rename collection %d: %w", id, err)
	}
	return nil
}

func (s *Store) DeleteCollection(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM collections WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete collection %d: %w", id, err)
	}
	return nil
}

// AddToCollection appends, so a curated order is built by adding in order.
func (s *Store) AddToCollection(ctx context.Context, collectionID, bookID int64) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO collection_books (collection_id, book_id, position)
		VALUES (?, ?, COALESCE(
			(SELECT MAX(position) + 1 FROM collection_books WHERE collection_id = ?), 0))`,
		collectionID, bookID, collectionID)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrAlreadyInside
		}
		return fmt.Errorf("add book %d to collection %d: %w", bookID, collectionID, err)
	}
	return nil
}

func (s *Store) RemoveFromCollection(ctx context.Context, collectionID, bookID int64) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM collection_books WHERE collection_id = ? AND book_id = ?`,
		collectionID, bookID)
	if err != nil {
		return fmt.Errorf("remove book %d from collection %d: %w", bookID, collectionID, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// OwnerOf reports a collection's owner, nil meaning curated, so the caller can
// build the policy before acting.
func (s *Store) OwnerOf(ctx context.Context, id int64) (*int64, error) {
	var owner sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT owner_id FROM collections WHERE id = ?`, id).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read owner of collection %d: %w", id, err)
	}
	if !owner.Valid {
		return nil, nil
	}
	return &owner.Int64, nil
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
