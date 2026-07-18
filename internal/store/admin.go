package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/amine123max/Mail/internal/model"
)

type AdminStats struct {
	Users           int `json:"users"`
	MailboxAccounts int `json:"mailboxAccounts"`
	ActiveGuests    int `json:"activeGuests"`
	Announcements   int `json:"announcements"`
}

type AdminActivity struct {
	Date          string `json:"date"`
	Users         int    `json:"users"`
	Accounts      int    `json:"accounts"`
	Guests        int    `json:"guests"`
	Announcements int    `json:"announcements"`
}

func (s *Store) ListAnnouncements(ctx context.Context, userID int64) ([]model.Announcement, int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT announcements.id,announcements.title,announcements.content,
		announcements.created_at,users.username,CASE WHEN announcement_reads.user_id IS NULL THEN 0 ELSE 1 END
		FROM announcements INNER JOIN users ON users.id=announcements.created_by
		LEFT JOIN announcement_reads ON announcement_reads.announcement_id=announcements.id AND announcement_reads.user_id=?
		ORDER BY announcements.created_at DESC,announcements.id DESC LIMIT 50`, userID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	announcements := make([]model.Announcement, 0)
	for rows.Next() {
		var item model.Announcement
		var read int
		if err := rows.Scan(&item.ID, &item.Title, &item.Content, &item.CreatedAt, &item.Author, &read); err != nil {
			return nil, 0, err
		}
		item.Read = read != 0
		announcements = append(announcements, item)
	}
	var unread int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM announcements WHERE NOT EXISTS
		(SELECT 1 FROM announcement_reads WHERE announcement_reads.announcement_id=announcements.id AND announcement_reads.user_id=?)`, userID).Scan(&unread); err != nil {
		return nil, 0, err
	}
	return announcements, unread, rows.Err()
}

func (s *Store) CreateAnnouncement(ctx context.Context, userID int64, title, content string) (model.Announcement, error) {
	createdAt := nowISO()
	result, err := s.db.ExecContext(ctx, "INSERT INTO announcements(title,content,created_by,created_at) VALUES(?,?,?,?)", title, content, userID, createdAt)
	if err != nil {
		return model.Announcement{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return model.Announcement{}, err
	}
	var item model.Announcement
	err = s.db.QueryRowContext(ctx, `SELECT announcements.id,announcements.title,announcements.content,
		announcements.created_at,users.username FROM announcements INNER JOIN users ON users.id=announcements.created_by
		WHERE announcements.id=?`, id).Scan(&item.ID, &item.Title, &item.Content, &item.CreatedAt, &item.Author)
	return item, err
}

func (s *Store) MarkAnnouncementsRead(ctx context.Context, userID int64) (int, error) {
	result, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO announcement_reads(announcement_id,user_id,read_at)
		SELECT id,?,? FROM announcements`, userID, nowISO())
	if err != nil {
		return 0, err
	}
	changes, err := result.RowsAffected()
	return int(changes), err
}

func (s *Store) GetAdminStats(ctx context.Context) (AdminStats, error) {
	var result AdminStats
	queries := []struct {
		query string
		value *int
	}{
		{"SELECT COUNT(*) FROM users", &result.Users},
		{"SELECT COUNT(*) FROM accounts", &result.MailboxAccounts},
		{"SELECT COUNT(*) FROM guest_sessions WHERE expires_at>?", &result.ActiveGuests},
		{"SELECT COUNT(*) FROM announcements", &result.Announcements},
	}
	for _, item := range queries {
		var err error
		if len(item.query) > 0 && item.query[len(item.query)-1] == '?' {
			err = s.db.QueryRowContext(ctx, item.query, nowISO()).Scan(item.value)
		} else {
			err = s.db.QueryRowContext(ctx, item.query).Scan(item.value)
		}
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

func (s *Store) GetAdminActivity(ctx context.Context, days int) ([]AdminActivity, error) {
	if days < 7 {
		days = 7
	}
	if days > 30 {
		days = 30
	}
	dates := make([]string, days)
	today := time.Now().UTC().Truncate(24 * time.Hour)
	for index := range dates {
		dates[index] = today.AddDate(0, 0, -(days - index - 1)).Format("2006-01-02")
	}
	firstDate := dates[0] + " 00:00:00"
	counts := make(map[string]map[string]int)
	for _, table := range []string{"users", "accounts", "guest_sessions", "announcements"} {
		rows, err := s.db.QueryContext(ctx, fmt.Sprintf("SELECT substr(created_at,1,10),COUNT(*) FROM %s WHERE created_at>=? GROUP BY substr(created_at,1,10)", table), firstDate)
		if err != nil {
			return nil, err
		}
		values := make(map[string]int)
		for rows.Next() {
			var date string
			var count int
			if err := rows.Scan(&date, &count); err != nil {
				_ = rows.Close()
				return nil, err
			}
			values[date] = count
		}
		_ = rows.Close()
		counts[table] = values
	}
	result := make([]AdminActivity, len(dates))
	for index, date := range dates {
		result[index] = AdminActivity{Date: date, Users: counts["users"][date], Accounts: counts["accounts"][date], Guests: counts["guest_sessions"][date], Announcements: counts["announcements"][date]}
	}
	return result, nil
}

func (s *Store) ListUsersForAdmin(ctx context.Context) ([]model.AdminUserSummary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT users.id,users.username,users.email_encrypted,users.is_admin,
		users.created_at,COUNT(accounts.id) FROM users LEFT JOIN accounts ON accounts.owner_key='user:'||users.id
		GROUP BY users.id ORDER BY users.created_at DESC,users.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]model.AdminUserSummary, 0)
	for rows.Next() {
		var item model.AdminUserSummary
		var encryptedEmail sql.NullString
		var administrator int
		if err := rows.Scan(&item.ID, &item.Username, &encryptedEmail, &administrator, &item.CreatedAt, &item.AccountCount); err != nil {
			return nil, err
		}
		item.Administrator = administrator != 0
		if encryptedEmail.Valid {
			email, err := s.box.Decrypt(encryptedEmail.String)
			if err != nil {
				return nil, err
			}
			item.Email = email
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
