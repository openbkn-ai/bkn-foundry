-- Copyright openbkn.ai
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

USE openbkn;

-- Unknown legacy languages stay NULL; never guess the language of custom text.
alter table t_agent_prompt_version
    add column if not exists f_source_language varchar(10) null after f_content;

-- These exact official Chinese bodies are known, including historical defaults.
-- Preserve administrator-selected language metadata and all existing translations.
update t_agent_prompt_version
set f_source_language = 'zh-CN'
where f_source_language is null
  and f_create_user in ('266c6a42-6131-4d62-8f39-853e7093701c', 'bdd59f76-19c3-58b0-bf5f-082c4c3cbddb')
  and sha2(f_content, 256) in (
      '0ab0a265fc0f0953d6f06b001ed0671893f20019ddde4049af1602a742e3e3f3',
      '0fc05ddef18daaa4adaccb41c9dc6f258eff99fde7f70813648feb3b03a7b3ae',
      '2bb793c87d28d4513e7c92211df008e779f058285df063e92afde63b6a9fd907',
      '6ef561c167134f3cae1abf1d3996b4a3830b1287e2161203f338294a52934ad2',
      '813dff66c8ea6f830601b173347936752f02e545d45781b4bb68266d13855eb1',
      '82951e10fedd5917845dbca8d97e9c26a9448a2763f2b828a714f9432d92c05a',
      '8a0906f82ba04b340056fe7bb63888a56a0c0beec6e61b4ac4d1f751ce745724',
      'ae1c828147e57c0f6db27029db35ed3efce3c9986deaa34e408c341bb70b1f8d',
      'b7bfaaf957df4f0c0339b03a2e47dd3cc9c810b2ee5b799f8f7c1fc4b6b072e5',
      'ba37512d65f3f10f78d9c396e362f32681289164b594d5474c1bd9dde7bc8694',
      '8cbd985c3fd9cd13ea12405ce569afd2f8f41f416524b83b0ad97e236e199244'
  );

-- Official Object Metric Designer, Project Drafter, Requirements and Answer Judge originals are English.
update t_agent_prompt_version
set f_source_language = 'en-US'
where f_source_language is null
  and f_create_user in ('266c6a42-6131-4d62-8f39-853e7093701c', 'bdd59f76-19c3-58b0-bf5f-082c4c3cbddb')
  and sha2(f_content, 256) in (
      '04f7548d8eb451eacfc718687c4362305c8d1c715725381a623a278234946d64',
      '4808bfa2955050cbb8eea9b15f82d023a855cd43c4153b540e2715531d70b198',
      '37614ef9fed4d6263fa6c4106da596b4efd7751c279d7e9468ce2755d4185d61',
      'e4900d7667d1a2267a85f201a12fdf93d3623b1929c85eea951143c0da7596b8',
      '847b459cb91977a69ab301cd66e04ead9166050647038c14bf4ff87e68b0e1b2',
      'd169780f870b47202eb4790acd480851f70de61a1a817cbfb580f23a8520accd',
      'bfcb1b21e62c17286514921221130dcc3b4fdb1581948d623b1207e0bc20ad51'
  );
