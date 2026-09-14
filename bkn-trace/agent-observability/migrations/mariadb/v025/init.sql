-- Copyright (c) 2026 OpenBKN
-- SPDX-License-Identifier: LicenseRef-OpenBKN
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.
--
-- Freeze the exact internal evidence-producer contract used by each operation
-- attempt. NULL is reserved for historical and uninstrumented producers; the
-- evidence projector treats those facts as execution_only.
ALTER TABLE bkn_trace_operation_call_facts
 ADD COLUMN IF NOT EXISTS capability_profile LONGTEXT NULL AFTER parent_operation_id;
