-- +goose Up
ALTER TABLE audit_records VALIDATE CONSTRAINT audit_action_valid_v18, VALIDATE CONSTRAINT audit_subject_valid_v18;
