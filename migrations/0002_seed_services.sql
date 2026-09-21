-- 0002_seed_services: default inventory for TicketNation's box.
-- Strategy config is data (ADR-0006): edit these rows (or insert your own
-- services) to manage a different host. Values verified against the tn-api,
-- tn-fe, and admin-fe compose files plus deploy/bluegreen-*.sh.
INSERT INTO services (id, name, strategy, compose_project, compose_dir, config) VALUES
('tn-api', 'tn-api', 'bluegreen', 'ticketnation', '/opt/ticketnation',
 '{"blue_service":"api-blue","green_service":"api-green","blue_target":"127.0.0.1:4101","green_target":"127.0.0.1:4102","legacy_target":"127.0.0.1:4000","nginx_conf":"/etc/nginx/sites-available/ticketnation","marker":"# BLUEGREEN-ACTIVE","blue_url":"http://127.0.0.1:4101/health","green_url":"http://127.0.0.1:4102/health","public_url":"https://api.ticketnation.ph/health"}'),
('tn-fe', 'tn-fe', 'recreate', 'ticketnation-fe', '/opt/ticketnation-fe',
 '{"service":"fe","health_url":"http://127.0.0.1:3000","public_url":"https://ticketnation.ph"}'),
('admin-fe', 'admin-fe', 'recreate', 'ticketnation-admin', '/opt/ticketnation-admin',
 '{"service":"app","health_url":"http://127.0.0.1:3002/health","public_url":"https://admin.ticketnation.ph/health"}');
