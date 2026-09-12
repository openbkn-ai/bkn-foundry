-- Copyright (c) 2026 OpenBKN
-- SPDX-License-Identifier: LicenseRef-OpenBKN
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.
--
-- A captured explanation may contain only the artifacts readable under the
-- current Core access profile. Partition the current-result cache by the
-- existing profile fingerprint; rows produced before this migration have an
-- empty fingerprint and are intentionally not reused by a scoped reader.
ALTER TABLE bkn_trace_ee_current_explanations
 ADD COLUMN access_profile_fingerprint VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' AFTER interaction_id;
ALTER TABLE bkn_trace_ee_current_explanations
 DROP PRIMARY KEY,
 ADD PRIMARY KEY (interaction_id, access_profile_fingerprint);
