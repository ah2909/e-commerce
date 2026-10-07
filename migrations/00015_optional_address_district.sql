-- +goose Up
ALTER TABLE customer_addresses ADD CONSTRAINT customer_addresses_text_valid_v15 CHECK (label = btrim(label) AND char_length(label) BETWEEN 1 AND 80 AND recipient_name = btrim(recipient_name) AND char_length(recipient_name) BETWEEN 1 AND 160 AND line1 = btrim(line1) AND char_length(line1) BETWEEN 1 AND 240 AND line2 = btrim(line2) AND char_length(line2) <= 240 AND ward = btrim(ward) AND char_length(ward) BETWEEN 1 AND 120 AND district = btrim(district) AND char_length(district) BETWEEN 0 AND 120 AND province = btrim(province) AND char_length(province) BETWEEN 1 AND 120 AND postal_code = btrim(postal_code) AND char_length(postal_code) <= 20) NOT VALID;
ALTER TABLE customer_addresses VALIDATE CONSTRAINT customer_addresses_text_valid_v15;
ALTER TABLE customer_addresses DROP CONSTRAINT customer_addresses_text_valid;
ALTER TABLE customer_addresses RENAME CONSTRAINT customer_addresses_text_valid_v15 TO customer_addresses_text_valid;

ALTER TABLE order_addresses ADD CONSTRAINT order_addresses_text_valid_v15 CHECK (recipient_name = btrim(recipient_name) AND char_length(recipient_name) BETWEEN 1 AND 160 AND phone = btrim(phone) AND char_length(phone) BETWEEN 8 AND 20 AND line1 = btrim(line1) AND char_length(line1) BETWEEN 1 AND 240 AND line2 = btrim(line2) AND char_length(line2) <= 240 AND ward = btrim(ward) AND char_length(ward) BETWEEN 1 AND 120 AND district = btrim(district) AND char_length(district) BETWEEN 0 AND 120 AND province = btrim(province) AND char_length(province) BETWEEN 1 AND 120 AND postal_code = btrim(postal_code) AND char_length(postal_code) <= 20) NOT VALID;
ALTER TABLE order_addresses VALIDATE CONSTRAINT order_addresses_text_valid_v15;
ALTER TABLE order_addresses DROP CONSTRAINT order_addresses_text_valid;
ALTER TABLE order_addresses RENAME CONSTRAINT order_addresses_text_valid_v15 TO order_addresses_text_valid;

