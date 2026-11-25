-- +goose Up
ALTER TABLE audit_records DROP CONSTRAINT audit_action_valid, DROP CONSTRAINT audit_subject_valid;
ALTER TABLE audit_records RENAME CONSTRAINT audit_action_valid_v18 TO audit_action_valid;
ALTER TABLE audit_records RENAME CONSTRAINT audit_subject_valid_v18 TO audit_subject_valid;
