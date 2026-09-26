-- name: GetPublicationEvent :one
SELECT id, status, current_revision
FROM events
WHERE organization_id = $1 AND id = $2;

-- name: ListPublicationSessions :many
SELECT s.id, s.status, s.inventory_mode, s.seat_map_version_id,
       v.status AS venue_status, sp.status AS space_status,
       COALESCE(sm.status, '') AS seat_map_status,
       COALESCE(pt.price_tier_count, 0)::bigint AS price_tier_count,
       COALESCE(sm.sellable_count, 0)::bigint AS seat_sellable_capacity,
       COALESCE(ga.pool_count, 0)::bigint AS ga_pool_count,
       COALESCE(ga.sellable_capacity, 0)::bigint AS ga_sellable_capacity,
       COALESCE(alloc.hard_quantity, 0)::bigint AS hard_allocation_quantity
FROM sessions s
JOIN venues v ON v.organization_id = s.organization_id AND v.id = s.venue_id
JOIN spaces sp ON sp.organization_id = s.organization_id AND sp.id = s.space_id
LEFT JOIN seat_map_versions sm ON sm.organization_id = s.organization_id AND sm.space_id = s.space_id AND sm.id = s.seat_map_version_id
LEFT JOIN (
    SELECT organization_id, session_id, count(*) AS price_tier_count
    FROM price_tiers
    GROUP BY organization_id, session_id
) pt ON pt.organization_id = s.organization_id AND pt.session_id = s.id
LEFT JOIN (
    SELECT organization_id, session_id, count(*)::bigint AS pool_count, sum(sellable_capacity)::bigint AS sellable_capacity
    FROM session_ga_pools
    GROUP BY organization_id, session_id
) ga ON ga.organization_id = s.organization_id AND ga.session_id = s.id
LEFT JOIN (
    SELECT organization_id, session_id, sum(quantity)::bigint AS hard_quantity
    FROM session_channel_allocations
    WHERE allocation_mode = 'hard_reserved'
    GROUP BY organization_id, session_id
) alloc ON alloc.organization_id = s.organization_id AND alloc.session_id = s.id
WHERE s.organization_id = $1 AND s.event_id = $2
ORDER BY s.starts_at ASC, s.id ASC;
