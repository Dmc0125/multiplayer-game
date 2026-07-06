begin;

create table users (
    id serial primary key,
    google_email text unique not null,
    google_name text not null,
    google_avatar_url text,
    created_at timestamp default now() not null
);

create table sessions (
    id text primary key,
    user_id int references users(id) not null,
    created_at timestamp default now() not null,
    expires_at timestamp default now() not null
);

create table stats (
    user_id int references users(id) not null primary key,
    wins int default 0 not null,
    losses int default 0 not null,
    sp_wins int default 0 not null,
    sp_losses int default 0 not null,
    created_at timestamp default now() not null,
    updated_at timestamp default now() not null
);

commit;
