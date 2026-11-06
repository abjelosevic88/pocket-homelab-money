-- Every expense now gets at least one guessed line (a bill is "Telefon", a
-- vague note "Namirnice"). Forget the earlier "no items" answers so the
-- hourly retry and "Guess them now" ask about those expenses again.
DELETE FROM item_guesses WHERE status = 'none';
