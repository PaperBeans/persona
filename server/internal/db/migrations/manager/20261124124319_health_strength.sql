-- Exercises: one movement on one piece of equipment. Deleting marks it; its sets are undone in the same transaction.
CREATE TABLE IF NOT EXISTS health_strength_exercises (
    id                uuid PRIMARY KEY,
    name              text NOT NULL CHECK (name <> '' AND char_length(name) <= 120),
    aliases           text[] NOT NULL DEFAULT '{}' CHECK (cardinality(aliases) <= 20),
    primary_muscles   text[] NOT NULL DEFAULT '{}',
    secondary_muscles text[] NOT NULL DEFAULT '{}',
    equipment         text NOT NULL DEFAULT '' CHECK (equipment IN ('', 'machine', 'dumbbell', 'barbell', 'cable', 'bodyweight', 'other')),
    weight_rule       text NOT NULL DEFAULT '' CHECK (weight_rule IN ('', 'stack', 'per_hand', 'with_bar', 'added')),
    setting           text NOT NULL DEFAULT '' CHECK (char_length(setting) <= 500),  -- seat / pin / handle note
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    deleted_at        timestamptz
);
CREATE UNIQUE INDEX IF NOT EXISTS health_strength_exercises_name ON health_strength_exercises (lower(name)) WHERE deleted_at IS NULL;

-- Strength workouts the iPhone uploaded (activity "Strength training" only): the window sets join.
CREATE TABLE IF NOT EXISTS health_strength_workouts (
    id         uuid PRIMARY KEY,                       -- the HealthKit workout UUID, lower case (workouts.go:123)
    start_at   timestamptz NOT NULL,
    end_at     timestamptz NOT NULL CHECK (end_at > start_at),
    report     jsonb NOT NULL CHECK (jsonb_typeof(report) = 'object'),  -- the WorkoutReport as received (aggregates only)
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS health_strength_workouts_start ON health_strength_workouts (start_at);

-- Confirmed sets only, under the intake entries' rules: the id is the client's, undo is a tombstone, every change takes a new seq.
CREATE SEQUENCE IF NOT EXISTS health_strength_set_seq;
CREATE TABLE IF NOT EXISTS health_strength_sets (
    id          uuid PRIMARY KEY,
    exercise_id uuid NOT NULL REFERENCES health_strength_exercises (id),
    weight      numeric(6,2) NOT NULL CHECK (weight >= 0 AND weight <= 1000),   -- kg, by the exercise's weight_rule
    reps        integer NOT NULL CHECK (reps >= 1 AND reps <= 1000),
    warmup      boolean NOT NULL DEFAULT false,
    at          timestamptz NOT NULL,
    workout_id  uuid REFERENCES health_strength_workouts (id),
    draft_id    uuid,                                   -- the draft it was saved from; NULL when logged directly
    seq         bigint NOT NULL DEFAULT nextval('health_strength_set_seq'),
    created_at  timestamptz NOT NULL DEFAULT now(),
    deleted_at  timestamptz
);
ALTER SEQUENCE health_strength_set_seq OWNED BY health_strength_sets.seq;
CREATE UNIQUE INDEX IF NOT EXISTS health_strength_sets_seq ON health_strength_sets (seq);
CREATE INDEX IF NOT EXISTS health_strength_sets_exercise ON health_strength_sets (exercise_id, at) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS health_strength_sets_at ON health_strength_sets (at) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS health_strength_sets_workout ON health_strength_sets (workout_id) WHERE deleted_at IS NULL;

-- Drafts: sets the model split out, or a session's plan, waiting for the owner. One row each, whole, with a revision.
CREATE TABLE IF NOT EXISTS health_strength_drafts (
    id           uuid PRIMARY KEY,
    kind         text NOT NULL CHECK (kind IN ('sets', 'plan')),
    status       text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'saved', 'discarded')),
    revision     bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    body         jsonb NOT NULL CHECK (jsonb_typeof(body) = 'object'),  -- {"sets":[{"exercise","weight","reps","warmup","at"}]}
    plan_day     date,                                  -- a plan: the owner's calendar day it is for
    conversation text NOT NULL,                         -- the Health conversation its card is in
    machine_id   text NOT NULL,
    card_id      text NOT NULL,                         -- assistant_views.id ("health-strength-" + draft id [+ "-" + n])
    card_version bigint NOT NULL CHECK (card_version > 0),  -- the card version that shows this revision
    created_at   timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL,
    settled_at   timestamptz,
    CHECK ((kind = 'plan') = (plan_day IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS health_strength_drafts_open ON health_strength_drafts (kind, plan_day, updated_at) WHERE status = 'open';
CREATE UNIQUE INDEX IF NOT EXISTS health_strength_drafts_card ON health_strength_drafts (card_id);
