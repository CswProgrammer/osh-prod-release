-- 移除旧版多余字段（icon 已改为 icon_code 后执行）
ALTER TABLE `osh_announcement` DROP COLUMN `color`;
ALTER TABLE `osh_announcement` DROP COLUMN `module`;
ALTER TABLE `osh_announcement` DROP COLUMN `is_top`;
