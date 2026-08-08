SELECT partman.undo_partition(
    p_parent_table := 'public.transactions',
    p_target_table := 'public.transactions_default',
    p_keep_table := false
);

DELETE FROM partman.part_config WHERE parent_table = 'public.transactions';

DROP TABLE IF EXISTS transactions CASCADE;
