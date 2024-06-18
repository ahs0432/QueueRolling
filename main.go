package main

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/proxy"
	"github.com/google/uuid"
	"github.com/redis/rueidis"
)

const host = "IPIPIPIP"
const proxyURL = "DOMAIN"
const userQueue = "userQueue"
const userCount = "userCount"
const lastScore = "lastScore"
const fixScore int64 = 100

type User struct {
	UID      string
	Counting int64
	MyCount  int64
}

func main() {
	if !fiber.IsChild() {
		go schedule()
	}

	app := fiber.New()

	app.Use("/*", func(c *fiber.Ctx) error {
		client := redisConnection()
		ctx := context.Background()
		c.Response().Header.Add("Cache-control", "no-cache")
		defer client.Close()
		defer ctx.Done()

		uid := c.Cookies("queue_uid")
		if uid == "" {
			uid = uuid.New().String()
			c.Cookie(&fiber.Cookie{
				Name:  "queue_uid",
				Value: uid,
			})
			fmt.Println(uid)
			return c.SendFile("html/queue_check.html")
		}

		if check, err := existsKey(ctx, client, fmt.Sprint(uid)); err != nil {
			panic(err)
		} else if !check {
			count, err := getInt64Key(ctx, client, userCount)
			if err != nil && !rueidis.IsRedisNil(err) {
				panic(err)
			}

			if count > fixScore {
				nowScore, err := getInt64Key(ctx, client, lastScore)
				if err != nil {
					panic(err)
				}

				usr := User{
					UID:      uid,
					Counting: count - fixScore,
					MyCount:  nowScore,
				}

				if rank, err := zrankSortedSet(ctx, client, userQueue, fmt.Sprint(uid)); err != nil {
					if !rueidis.IsRedisNil(err) {
						panic(err)
					} else {
						if _, err := zaddSortedSet(ctx, client, userQueue, fmt.Sprint(uid), nowScore); err != nil {
							panic(err)
						}
						incrKey(ctx, client, userCount, 1)
					}
				} else {
					usr.MyCount = rank
					//return c.SendString(fmt.Sprintf("당신은 잡혔습니다.\n세션 이름: %v\n현재 접속 대기열: %d\n당신의 순번: %d", uid, count-fixScore, rank))
				}

				fmt.Println(usr)
				return c.Render("html/queue.html", fiber.Map{"User": usr})

				//return c.SendString(fmt.Sprintf("당신은 잡혔습니다.\n세션 이름: %v\n현재 접속 대기열: %d\n당신의 순번: %d", uid, count-fixScore, nowScore))
			} else {
				incrKey(ctx, client, userCount, 1)
				if _, err := setKey(ctx, client, fmt.Sprint(uid), "1"); err != nil {
					panic(err)
				}

				if _, err := setExpireKey(ctx, client, fmt.Sprint(uid), 300); err != nil {
					panic(err)
				}
			}
		} else {
			if _, err := setExpireKey(ctx, client, fmt.Sprint(uid), 300); err != nil {
				panic(err)
			}
		}

		c.Request().Header.Add("X-Forwarded-For", c.IP())
		//fmt.Println(c.IP(), c.IPs())
		proxtURL := strings.Replace(c.Request().URI().String(), c.BaseURL(), proxyURL, 1)
		return proxy.Do(c, proxtURL)
	})
	app.Listen(":80")
}

func schedule() {
	ticker := time.NewTicker(time.Millisecond * 1000)
	defer ticker.Stop()

	for t := range ticker.C {
		if t.Format("05") == "00" {
			go timeDecr()
		}
	}
}

func timeDecr() {
	c := redisConnection()
	ctx := context.Background()
	defer c.Close()
	defer ctx.Done()

	if check, err := existsKey(ctx, c, userCount); err != nil {
		panic(err)
	} else if !check {
		if _, err := setKey(ctx, c, userCount, "0"); err != nil {
			panic(err)
		}
	}

	if count, err := getInt64Key(ctx, c, userCount); err != nil {
		if !rueidis.IsRedisNil(err) {
			panic(err)
		} else {
			if _, err := setKey(ctx, c, userCount, "0"); err != nil {
				panic(err)
			}
		}
	} else {
		if count > fixScore {
			count = fixScore
		} else {
			if _, err := setKey(ctx, c, lastScore, "0"); err != nil {
				panic(err)
			}
		}

		if _, err := decrKey(ctx, c, userCount, count); err != nil {
			panic(err)
		}
	}

	if queue, err := zrangeSortedSet(ctx, c, userQueue, fixScore); err != nil {
		if !rueidis.IsRedisNil(err) {
			panic(err)
		}
	} else {
		for i := 0; i < len(queue); i++ {
			if _, err := zremSortedSet(ctx, c, userQueue, queue[i]); err != nil {
				if !rueidis.IsRedisNil(err) {
					panic(err)
				}
			}

			if _, err := setKey(ctx, c, queue[i], "1"); err != nil {
				panic(err)
			}
		}
	}

	fmt.Println("[DEBUG] Rolling")
}

func redisConnection() rueidis.Client {
	client, err := rueidis.NewClient(rueidis.ClientOption{InitAddress: []string{host}})
	if err != nil {
		log.Fatal(err.Error())
		panic(err)
	}

	ctx := context.Background()
	defer ctx.Done()

	_, err = client.Do(ctx, client.B().Ping().Build()).ToString()
	if err != nil {
		log.Fatal(err.Error())
		panic(err)
	}
	return client
}

func getInt64Key(ctx context.Context, client rueidis.Client, key string) (int64, error) {
	return client.Do(ctx, client.B().Get().Key(key).Build()).AsInt64()
}

func setKey(ctx context.Context, client rueidis.Client, key string, val string) (bool, error) {
	return client.Do(ctx, client.B().Set().Key(key).Value(val).Build()).AsBool()
}

func existsKey(ctx context.Context, client rueidis.Client, key string) (bool, error) {
	return client.Do(ctx, client.B().Exists().Key(key).Build()).AsBool()
}

func setExpireKey(ctx context.Context, client rueidis.Client, key string, ttl int64) (bool, error) {
	return client.Do(ctx, client.B().Expire().Key(key).Seconds(ttl).Build()).AsBool()
}

func incrKey(ctx context.Context, client rueidis.Client, key string, val int64) (int64, error) {
	return client.Do(ctx, client.B().Incrby().Key(key).Increment(val).Build()).AsInt64()
}

func decrKey(ctx context.Context, client rueidis.Client, key string, val int64) (int64, error) {
	return client.Do(ctx, client.B().Decrby().Key(key).Decrement(val).Build()).AsInt64()
}

func zrankSortedSet(ctx context.Context, client rueidis.Client, key, member string) (int64, error) {
	return client.Do(ctx, client.B().Zrank().Key(key).Member(member).Build()).AsInt64()
}

func zaddSortedSet(ctx context.Context, client rueidis.Client, key, member string, score int64) (bool, error) {
	return client.Do(ctx, client.B().Zadd().Key(key).ScoreMember().ScoreMember(float64(score), member).Build()).AsBool()
}

func zremSortedSet(ctx context.Context, client rueidis.Client, key, member string) (bool, error) {
	return client.Do(ctx, client.B().Zrem().Key(key).Member(member).Build()).AsBool()
}

func zrangeSortedSet(ctx context.Context, client rueidis.Client, key string, val int64) ([]string, error) {
	return client.Do(ctx, client.B().Zrange().Key(key).Min("0").Max(strconv.FormatInt(val, 10)).Build()).AsStrSlice()
}
