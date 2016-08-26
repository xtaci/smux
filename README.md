# smux
simple multiplexing

# goals
1. receive window is shared among the streams.
2. precise flow control.
3. precise memory control.
4. maximized payload.
5. optimized for high-speed streams.

```
FRAMETYPE=Data, OPTIONS=(flagSYN, flagACK, flagFIN, flagRST)
+---------------+--------------+------------+--------------+--------------------------------+
|               |              |            |              |                                |
| FRAMETYPE(1B) | OPTIONS(1B)  | LENGTH(2B) | STREAMID(4B) | DATA(MAX 64K)                  |
|               |              |            |              |                                |
+---------------+--------------+------------+--------------+--------------------------------+

FRAMETYPE=WindowUpdate
+---------------+--------------+------------+
|               |                           |
| FRAMETYPE(1B) |  WINDOWSIZE(4B)           |
|               |                           |
+---------------+--------------+------------+

FRAMETYPE=Ping, OPTIONS=(flagSYN, flagACK)
+---------------+--------------+------------+------------+
|               |              |                         |
| FRAMETYPE(1B) | OPTIONS(1B)  | PINGID(4B)              |
|               |              |                         |
+---------------+--------------+------------+------------+

FRAMETYPE=GoAway 
+---------------+---------------+
|               |               |
| FRAMETYPE(1B) | ERROR(2B)     |
|               |               |
+---------------+---------------+
```

# status
in-progress
