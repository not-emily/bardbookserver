package scan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/not-emily/bardbookserver/internal/metadata"
	"github.com/not-emily/bardbookserver/internal/taxonomy"
)

// applySubjects writes the genre facet and keeps what it could not map. The
// suggestion is written whatever the locks say, since nothing reads it as a
// decision.
// classifier is loaded once per scan, so a mapping an admin entered applies to
// every book the scan touches without a query each time.
func (s *Scanner) classifier(ctx context.Context) taxonomy.Classifier {
	s.once.Do(func() {
		aliases, err := loadAliases(ctx, s.db)
		if err != nil {
			s.logger.Error("read genre aliases", "error", err)
		}
		s.aliases = aliases
	})
	return taxonomy.NewClassifier(s.aliases)
}

func loadAliases(ctx context.Context, database *sql.DB) (map[string]string, error) {
	rows, err := database.QueryContext(ctx, `SELECT subject, genre_slug FROM genre_aliases`)
	if err != nil {
		return nil, fmt.Errorf("read genre aliases: %w", err)
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var subject, slug string
		if err := rows.Scan(&subject, &slug); err != nil {
			return nil, fmt.Errorf("scan genre alias: %w", err)
		}
		out[subject] = slug
	}
	return out, rows.Err()
}

func applySubjects(ctx context.Context, tx *sql.Tx, bookID int64,
	md metadata.Metadata, locked bool, classifier taxonomy.Classifier) error {

	if err := setSuggestedAge(ctx, tx, bookID, md); err != nil {
		return err
	}
	if locked || len(md.Subjects) == 0 {
		return nil
	}

	slugs, raw := classifier.Classify(md.Subjects)
	if err := replaceGenres(ctx, tx, bookID, slugs); err != nil {
		return err
	}
	return replaceRawSubjects(ctx, tx, bookID, raw)
}

func setSuggestedAge(ctx context.Context, tx *sql.Tx, bookID int64, md metadata.Metadata) error {
	suggested := taxonomy.SuggestAge(md.Subjects, md.Mature)
	if suggested == nil {
		return nil
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE books SET suggested_min_age = ? WHERE id = ?`, *suggested, bookID); err != nil {
		return fmt.Errorf("suggest an age for book %d: %w", bookID, err)
	}
	return nil
}

func replaceGenres(ctx context.Context, tx *sql.Tx, bookID int64, slugs []string) error {
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM book_genres WHERE book_id = ?`, bookID); err != nil {
		return fmt.Errorf("clear genres of book %d: %w", bookID, err)
	}

	for _, slug := range slugs {
		genreID, err := genreIDFor(ctx, tx, slug)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO book_genres (book_id, genre_id) VALUES (?, ?)
			 ON CONFLICT DO NOTHING`, bookID, genreID); err != nil {
			return fmt.Errorf("file book %d under %q: %w", bookID, slug, err)
		}
	}
	return nil
}

// genreIDFor inserts the canonical genre the first time a book needs it, which
// is why there is no seeding pass: the table cannot drift from the list, and
// the facet lists only genres something is actually filed under.
func genreIDFor(ctx context.Context, tx *sql.Tx, slug string) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM genres WHERE slug = ?`, slug).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("look up genre %q: %w", slug, err)
	}

	genre, ok := taxonomy.BySlug(slug)
	if !ok {
		return 0, fmt.Errorf("genre %q is not in the canonical list", slug)
	}

	out, err := tx.ExecContext(ctx,
		`INSERT INTO genres (name, slug) VALUES (?, ?)`, genre.Name, genre.Slug)
	if err != nil {
		return 0, fmt.Errorf("insert genre %q: %w", slug, err)
	}
	return out.LastInsertId()
}

func replaceRawSubjects(ctx context.Context, tx *sql.Tx, bookID int64, raw []string) error {
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM raw_subjects WHERE book_id = ?`, bookID); err != nil {
		return fmt.Errorf("clear raw subjects of book %d: %w", bookID, err)
	}

	for _, subject := range raw {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO raw_subjects (book_id, subject) VALUES (?, ?)
			 ON CONFLICT DO NOTHING`, bookID, subject); err != nil {
			return fmt.Errorf("keep subject %q for book %d: %w", subject, bookID, err)
		}
	}
	return nil
}
