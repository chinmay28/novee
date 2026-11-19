---
name: news-headlines
description: Get today's top news headlines (overall, by topic like technology/business/sports, from a specific outlet like BBC/NPR/NYT, or news about a given subject) from RSS feeds. Use for "headlines", "top stories", "what's in the news", "latest news on X".
metadata:
  {
    "openclaw":
      {
        "emoji": "📰",
        "os": ["linux"],
        "requires": { "bins": ["python3"] },
      },
  }
---

# news-headlines

Use this skill whenever the user asks for news headlines, top stories, what is happening today, or the latest news
about a subject. Do **not** use `web_search` for headlines: search engines return news *websites*, not stories.
This script reads real RSS feeds, merges outlets covering the same story into one entry, and prints actual
headlines with source, age and link (an `also:` line names other outlets on the same story). No API key is needed.

## How to run

```bash
python3 {baseDir}/scripts/headlines.py                 # top 10 overall (Google News, US/English)
python3 {baseDir}/scripts/headlines.py -n 5            # only 5
python3 {baseDir}/scripts/headlines.py -t technology   # section: world nation business technology entertainment sports science health
python3 {baseDir}/scripts/headlines.py -s bbc          # outlet: google bbc bbc-world npr nytimes guardian aljazeera hackernews
python3 {baseDir}/scripts/headlines.py -q "nvidia earnings"   # news about a subject
python3 {baseDir}/scripts/headlines.py --country IN --lang en  # another region's Google News
```

Run exactly one command per request (pick the flags from the user's wording), then reply with the script's output.

## How to answer

- Reply with the script output as printed: numbered title, source and age in parentheses, optional `also:` line,
  link on the next line. Do not strip the links or ages, and do not add stories the script did not print.
- Do not invent, reorder, merge or "summarize" headlines. If the user wants a summary of one story, call
  `web_fetch` on that story's link and summarize what it returns.
- If the script prints `ERROR`, say the feed could not be reached and try `-s bbc` or `-s npr` once before giving up.
- Default count is 10; honour the number the user asks for (max 50).
