DROP TRIGGER task_events_note_participant ON task_events;
DROP TRIGGER attachments_note_participant ON attachments;
DROP TRIGGER comments_note_participant ON comments;
DROP TRIGGER tasks_note_participants ON tasks;
DROP FUNCTION task_participants_on_act();
DROP FUNCTION task_participants_on_task();
DROP FUNCTION note_participant(UUID, UUID, TIMESTAMPTZ);
DROP TABLE task_participants;
