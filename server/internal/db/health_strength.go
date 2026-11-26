package db

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/nwverse/persona/server/internal/assistantwire"
)

type StrengthExercise = assistantwire.StrengthExercise
type StrengthSet = assistantwire.StrengthSet
type StrengthSetFilter = assistantwire.StrengthSetFilter
type StrengthWorkout = assistantwire.StrengthWorkout
type StrengthDraft = assistantwire.StrengthDraft
type StrengthCard = assistantwire.StrengthCard

var (
	ErrHealthStrengthName         = assistantwire.ErrHealthStrengthName
	ErrHealthStrengthDraftStale   = assistantwire.ErrHealthStrengthDraftStale
	ErrHealthStrengthDraftSettled = assistantwire.ErrHealthStrengthDraftSettled
)

const strengthExerciseColumns = `id::text, name, aliases, primary_muscles, secondary_muscles, equipment, weight_rule, setting, created_at, updated_at, deleted_at`
const strengthSetColumns = `id::text, exercise_id::text, weight::double precision, reps, warmup, at, workout_id::text, draft_id::text, seq, created_at, deleted_at`
const strengthDraftColumns = `id::text, kind, status, revision, body, plan_day, conversation, machine_id, card_id, card_version, created_at, updated_at, settled_at`

func scanStrengthExercise(row pgx.Row) (v StrengthExercise, err error) {
	err = row.Scan(&v.ID, &v.Name, &v.Aliases, &v.Primary, &v.Secondary, &v.Equipment, &v.WeightRule, &v.Setting, &v.CreatedAt, &v.UpdatedAt, &v.DeletedAt)
	return
}
func scanStrengthSet(row pgx.Row) (v StrengthSet, err error) {
	err = row.Scan(&v.ID, &v.ExerciseID, &v.Weight, &v.Reps, &v.Warmup, &v.At, &v.WorkoutID, &v.DraftID, &v.Seq, &v.CreatedAt, &v.DeletedAt)
	return
}
func scanStrengthDraft(row pgx.Row) (v StrengthDraft, err error) {
	err = row.Scan(&v.ID, &v.Kind, &v.Status, &v.Revision, &v.Body, &v.PlanDay, &v.Conversation, &v.MachineID, &v.CardID, &v.CardVersion, &v.CreatedAt, &v.UpdatedAt, &v.SettledAt)
	return
}
func strengthLock(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('health_strength_sets', 0))`)
	return err
}
func strengthViewLocks(ctx context.Context, tx pgx.Tx, conversations ...string) error {
	slices.Sort(conversations)
	for _, c := range slices.Compact(conversations) {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "assistant_views:"+c); err != nil {
			return err
		}
	}
	return nil
}
func (d *DB) HealthStrengthExercises(ctx context.Context, deleted bool) ([]StrengthExercise, error) {
	rows, err := d.pool.Query(ctx, `SELECT `+strengthExerciseColumns+` FROM health_strength_exercises WHERE $1 OR deleted_at IS NULL ORDER BY lower(name), id`, deleted)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StrengthExercise{}
	for rows.Next() {
		v, err := scanStrengthExercise(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (d *DB) HealthStrengthExerciseSave(ctx context.Context, v StrengthExercise) (saved StrengthExercise, changed bool, err error) {
	if v.Aliases == nil {
		v.Aliases = []string{}
	}
	if v.Primary == nil {
		v.Primary = []string{}
	}
	if v.Secondary == nil {
		v.Secondary = []string{}
	}
	saved, err = scanStrengthExercise(d.pool.QueryRow(ctx, `INSERT INTO health_strength_exercises (id,name,aliases,primary_muscles,secondary_muscles,equipment,weight_rule,setting)
 VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (id) DO UPDATE SET name=$2,aliases=$3,primary_muscles=$4,secondary_muscles=$5,equipment=$6,weight_rule=$7,setting=$8,updated_at=now() WHERE health_strength_exercises.deleted_at IS NULL RETURNING `+strengthExerciseColumns, v.ID, v.Name, v.Aliases, v.Primary, v.Secondary, v.Equipment, v.WeightRule, v.Setting))
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		err = ErrHealthStrengthName
	}
	return saved, err == nil, err
}
func (d *DB) HealthStrengthExerciseDelete(ctx context.Context, id string) (changed bool, err error) {
	err = pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		if err := strengthLock(ctx, tx); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE health_strength_exercises SET deleted_at=now(),updated_at=now() WHERE id=$1 AND deleted_at IS NULL`, id)
		if err != nil {
			return err
		}
		changed = tag.RowsAffected() > 0
		_, err = tx.Exec(ctx, `UPDATE health_strength_sets SET deleted_at=now(),seq=nextval('health_strength_set_seq') WHERE exercise_id=$1 AND deleted_at IS NULL`, id)
		return err
	})
	return
}

// Both arrival orders use the same inclusive workout window.
const strengthWindow = `$1 BETWEEN start_at - interval '30 minutes' AND end_at + interval '3 hours'`

func strengthSetAddTx(ctx context.Context, tx pgx.Tx, sets []StrengthSet) ([]StrengthSet, error) {
	out := []StrengthSet{}
	for _, v := range sets {
		// Existing ids return their original row, including an undo tombstone.
		old, err := scanStrengthSet(tx.QueryRow(ctx, `SELECT `+strengthSetColumns+` FROM health_strength_sets WHERE id=$1`, v.ID))
		if err == nil {
			out = append(out, old)
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		var active bool
		if err := tx.QueryRow(ctx, `SELECT deleted_at IS NULL FROM health_strength_exercises WHERE id=$1 FOR SHARE`, v.ExerciseID).Scan(&active); err != nil {
			return nil, err
		}
		if !active {
			return nil, errors.New("strength exercise is deleted")
		}
		if v.WorkoutID == nil {
			var id string
			err := tx.QueryRow(ctx, `SELECT id::text FROM health_strength_workouts WHERE `+strengthWindow+` ORDER BY start_at DESC,id LIMIT 1`, v.At).Scan(&id)
			if err == nil {
				v.WorkoutID = &id
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return nil, err
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO health_strength_sets(id,exercise_id,weight,reps,warmup,at,workout_id,draft_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(id) DO NOTHING`, v.ID, v.ExerciseID, v.Weight, v.Reps, v.Warmup, v.At, v.WorkoutID, v.DraftID); err != nil {
			return nil, err
		}
		saved, err := scanStrengthSet(tx.QueryRow(ctx, `SELECT `+strengthSetColumns+` FROM health_strength_sets WHERE id=$1`, v.ID))
		if err != nil {
			return nil, err
		}
		out = append(out, saved)
	}
	return out, nil
}
func (d *DB) HealthStrengthSetAdd(ctx context.Context, sets []StrengthSet) (out []StrengthSet, err error) {
	err = pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		if err := strengthLock(ctx, tx); err != nil {
			return err
		}
		out, err = strengthSetAddTx(ctx, tx, sets)
		return err
	})
	return
}
func (d *DB) HealthStrengthSetUndo(ctx context.Context, id string) (saved *StrengthSet, changed bool, err error) {
	err = pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		if err := strengthLock(ctx, tx); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE health_strength_sets SET deleted_at=now(),seq=nextval('health_strength_set_seq') WHERE id=$1 AND deleted_at IS NULL`, id)
		if err != nil {
			return err
		}
		changed = tag.RowsAffected() > 0
		v, err := scanStrengthSet(tx.QueryRow(ctx, `SELECT `+strengthSetColumns+` FROM health_strength_sets WHERE id=$1`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err == nil {
			saved = &v
		}
		return err
	})
	return
}
func (d *DB) strengthSets(ctx context.Context, query string, args ...any) ([]StrengthSet, error) {
	rows, err := d.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StrengthSet{}
	for rows.Next() {
		v, err := scanStrengthSet(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (d *DB) HealthStrengthSets(ctx context.Context, f StrengthSetFilter) ([]StrengthSet, error) {
	var from, until *time.Time
	if !f.From.IsZero() {
		from = &f.From
	}
	if !f.Until.IsZero() {
		until = &f.Until
	}
	return d.strengthSets(ctx, `SELECT `+strengthSetColumns+` FROM health_strength_sets WHERE deleted_at IS NULL AND ($1='' OR exercise_id::text=$1) AND ($2='' OR workout_id::text=$2) AND ($3::timestamptz IS NULL OR at >= $3) AND ($4::timestamptz IS NULL OR at < $4) ORDER BY at DESC,id LIMIT NULLIF($5::integer,0)`, f.Exercise, f.Workout, from, until, max(f.Limit, 0))
}
func (d *DB) HealthStrengthSetChanges(ctx context.Context, after int64, limit int) ([]StrengthSet, error) {
	return d.strengthSets(ctx, `SELECT `+strengthSetColumns+` FROM health_strength_sets WHERE seq>$1 ORDER BY seq LIMIT $2`, after, limit)
}
func (d *DB) HealthStrengthWorkoutAdd(ctx context.Context, w StrengthWorkout) (linked int, err error) {
	err = pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		if err := strengthLock(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO health_strength_workouts(id,start_at,end_at,report) VALUES($1,$2,$3,$4) ON CONFLICT(id) DO NOTHING`, w.ID, w.Start, w.End, []byte(w.Report)); err != nil {
			return err
		}
		// Read the persisted window on retries, never the caller's changed measurements.
		tag, err := tx.Exec(ctx, `UPDATE health_strength_sets s SET workout_id=$1,seq=nextval('health_strength_set_seq') FROM health_strength_workouts w WHERE w.id=$1 AND s.workout_id IS NULL AND s.deleted_at IS NULL AND s.at BETWEEN w.start_at-interval '30 minutes' AND w.end_at+interval '3 hours'`, w.ID)
		linked = int(tag.RowsAffected())
		return err
	})
	return
}
func (d *DB) HealthStrengthWorkout(ctx context.Context, id string) (*StrengthWorkout, error) {
	var v StrengthWorkout
	err := d.pool.QueryRow(ctx, `SELECT id::text,start_at,end_at,report FROM health_strength_workouts WHERE id=$1`, id).Scan(&v.ID, &v.Start, &v.End, &v.Report)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &v, err
}

// HealthStrengthWorkoutLatest selects the latest start in an owner-local day's half-open range.
func (d *DB) HealthStrengthWorkoutLatest(ctx context.Context, from, until time.Time) (*StrengthWorkout, error) {
	var v StrengthWorkout
	err := d.pool.QueryRow(ctx, `SELECT id::text,start_at,end_at,report FROM health_strength_workouts WHERE start_at >= $1 AND start_at < $2 ORDER BY start_at DESC,id LIMIT 1`, from, until).Scan(&v.ID, &v.Start, &v.End, &v.Report)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &v, err
}

func strengthCreateCard(ctx context.Context, tx pgx.Tx, c StrengthCard) error {
	return createAssistantViewTx(ctx, tx, AssistantViewRow{ID: c.ID, Conversation: c.Conversation, MachineID: c.MachineID, View: c.View, Version: c.Version, CreatedAt: c.At, UpdatedAt: c.At}, FeedRow{ID: c.FeedID, Conversation: c.Conversation, At: c.At, Kind: "view", Ref: c.ID})
}
func (d *DB) HealthStrengthDraftCreate(ctx context.Context, v StrengthDraft, c StrengthCard) error {
	if v.CardID != c.ID || v.CardVersion != c.Version || v.Conversation != c.Conversation || v.MachineID != c.MachineID {
		return ErrHealthStrengthDraftStale
	}
	return pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		if err := strengthViewLocks(ctx, tx, c.Conversation); err != nil {
			return err
		}
		if err := strengthCreateCard(ctx, tx, c); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO health_strength_drafts(id,kind,status,revision,body,plan_day,conversation,machine_id,card_id,card_version,created_at,updated_at) VALUES($1,$2,'open',1,$3,$4,$5,$6,$7,$8,$9,$9)`, v.ID, v.Kind, []byte(v.Body), v.PlanDay, v.Conversation, v.MachineID, v.CardID, v.CardVersion, c.At)
		return err
	})
}
func (d *DB) strengthDraft(ctx context.Context, field, id string) (*StrengthDraft, error) {
	v, err := scanStrengthDraft(d.pool.QueryRow(ctx, `SELECT `+strengthDraftColumns+` FROM health_strength_drafts WHERE `+field+`=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &v, err
}
func (d *DB) HealthStrengthDraft(ctx context.Context, id string) (*StrengthDraft, error) {
	return d.strengthDraft(ctx, "id", id)
}
func (d *DB) HealthStrengthDraftByCard(ctx context.Context, id string) (*StrengthDraft, error) {
	return d.strengthDraft(ctx, "card_id", id)
}
func (d *DB) HealthStrengthDrafts(ctx context.Context, status string) ([]StrengthDraft, error) {
	rows, err := d.pool.Query(ctx, `SELECT `+strengthDraftColumns+` FROM health_strength_drafts WHERE $1='' OR status=$1 ORDER BY updated_at DESC,id`, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StrengthDraft{}
	for rows.Next() {
		v, err := scanStrengthDraft(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Lock order is sets, conversation(s), draft. Reading the owner before locking is checked again under the row lock.
func (d *DB) strengthDraftChange(ctx context.Context, id string, rev int64, body, view json.RawMessage, at time.Time, status string, sets []StrengthSet, move *StrengthCard) (v StrengthDraft, c StrengthCard, saved []StrengthSet, err error) {
	err = pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		if err := strengthLock(ctx, tx); err != nil {
			return err
		}
		var conv string
		if err := tx.QueryRow(ctx, `SELECT conversation FROM health_strength_drafts WHERE id=$1`, id).Scan(&conv); err != nil {
			return err
		}
		convs := []string{conv}
		if move != nil {
			convs = append(convs, move.Conversation)
		}
		if err := strengthViewLocks(ctx, tx, convs...); err != nil {
			return err
		}
		var err error
		v, err = scanStrengthDraft(tx.QueryRow(ctx, `SELECT `+strengthDraftColumns+` FROM health_strength_drafts WHERE id=$1 FOR UPDATE`, id))
		if err != nil {
			return err
		}
		if v.Conversation != conv {
			return ErrHealthStrengthDraftStale
		}
		if v.Status != "open" {
			return ErrHealthStrengthDraftSettled
		}
		if v.Revision != rev {
			return ErrHealthStrengthDraftStale
		}
		row, err := updateAssistantViewTx(ctx, tx, v.Conversation, v.CardID, v.CardVersion, view, at)
		if err != nil {
			if errors.Is(err, ErrAssistantViewVersion) || errors.Is(err, ErrAssistantViewNotFound) {
				return ErrHealthStrengthDraftStale
			}
			return err
		}
		c = StrengthCard{ID: row.ID, Conversation: row.Conversation, MachineID: row.MachineID, View: row.View, Version: row.Version, At: row.UpdatedAt, CreatedAt: row.CreatedAt}
		if status == "saved" {
			saved, err = strengthSetAddTx(ctx, tx, sets)
			if err != nil {
				return err
			}
		}
		if len(body) > 0 {
			v.Body = body
		}
		v.Status = status
		v.Revision++
		v.CardVersion = c.Version
		v.UpdatedAt = at
		if status != "open" {
			v.SettledAt = &at
		}
		if move != nil {
			if err := strengthCreateCard(ctx, tx, *move); err != nil {
				return err
			}
			v.Conversation, v.MachineID, v.CardID, v.CardVersion = move.Conversation, move.MachineID, move.ID, move.Version
		}
		_, err = tx.Exec(ctx, `UPDATE health_strength_drafts SET status=$2,revision=$3,body=$4,conversation=$5,machine_id=$6,card_id=$7,card_version=$8,updated_at=$9,settled_at=$10 WHERE id=$1`, id, v.Status, v.Revision, []byte(v.Body), v.Conversation, v.MachineID, v.CardID, v.CardVersion, at, v.SettledAt)
		return err
	})
	return
}
func (d *DB) HealthStrengthDraftEdit(ctx context.Context, id string, rev int64, body, view json.RawMessage, at time.Time) (StrengthDraft, StrengthCard, error) {
	v, c, _, err := d.strengthDraftChange(ctx, id, rev, body, view, at, "open", nil, nil)
	return v, c, err
}
func (d *DB) HealthStrengthDraftConfirm(ctx context.Context, id string, rev int64, sets []StrengthSet, body, view json.RawMessage, at time.Time) (StrengthDraft, StrengthCard, []StrengthSet, error) {
	return d.strengthDraftChange(ctx, id, rev, body, view, at, "saved", sets, nil)
}
func (d *DB) HealthStrengthDraftDiscard(ctx context.Context, id string, rev int64, body, view json.RawMessage, at time.Time) (StrengthDraft, StrengthCard, error) {
	v, c, _, err := d.strengthDraftChange(ctx, id, rev, body, view, at, "discarded", nil, nil)
	return v, c, err
}
func (d *DB) HealthStrengthDraftMove(ctx context.Context, id string, rev int64, oldView json.RawMessage, card StrengthCard) (StrengthDraft, error) {
	v, _, _, err := d.strengthDraftChange(ctx, id, rev, nil, oldView, card.At, "open", nil, &card)
	return v, err
}
