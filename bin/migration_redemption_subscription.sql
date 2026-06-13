ALTER TABLE redemptions ADD COLUMN type varchar(32) DEFAULT 'quota';
ALTER TABLE redemptions ADD COLUMN subscription_plan_id int DEFAULT 0;
