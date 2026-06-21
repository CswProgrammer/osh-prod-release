-- 旧表 icon 列改名为 icon_code（仅当仍存在 icon 列时执行一次）
ALTER TABLE `osh_announcement`
  CHANGE COLUMN `icon` `icon_code` varchar(50) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs DEFAULT NULL COMMENT '图标编码';
