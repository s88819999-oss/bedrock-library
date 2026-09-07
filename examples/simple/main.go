package main

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/patyhank/bedrock-library/bot"
	"github.com/sandertv/gophertunnel/minecraft/auth"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
	"github.com/sandertv/gophertunnel/minecraft/text"
)

var recvPMRegex = regexp.MustCompile("^\\[(\\w+) -> 您]\\s([\\s\\S]+)")
var recvTeleportRegex = regexp.MustCompile("^\\[系統] (\\w+) 想要傳送到 你 的位置。")
var recvTeleportHereRegex = regexp.MustCompile("^\\[系統] (\\w+) 想要你傳送到 該玩家 的位置。")

var owners = []string{"j850728w"}
var ads = []string{"test", "test1"}
var deathRecoveryMu sync.Mutex
var deathRecoveryRunning bool
var waterFillRunning bool
var waterFillCancel context.CancelFunc

func main() {
	token, err := auth.RequestLiveToken()
	if err != nil {
		panic(err)
	}

	client := bot.NewClient()
	for {
		err = client.ConnectTo(bot.ClientConfig{
			Address: "bedrock.mcfallout.net:19132",
			Token:   token,
		})
		if err == nil {
			break
		}
		log.Printf("連線失敗，15 秒後重試: %v", err)
		time.Sleep(15 * time.Second)
		client = bot.NewClient()
	}

	(&bot.EventsListener{}).Attach(client)

	bot.AddListener(client, bot.PacketHandler[*packet.Text]{ // Listen any text packet
		Priority: 1024,
		F: func(client *bot.Client, p *packet.Text) error {
			log.Println(text.ANSI(p.Message)) // Print the colored message to the console
			cleanString := text.Clean(p.Message)
			if strings.Contains(cleanString, client.Conn.IdentityData().DisplayName+" 被") {
				deathRecoveryMu.Lock()
				shouldRecover := !deathRecoveryRunning
				deathRecoveryRunning = true
				deathRecoveryMu.Unlock()
				if shouldRecover {
					go func() {
						defer func() {
							deathRecoveryMu.Lock()
							deathRecoveryRunning = false
							deathRecoveryMu.Unlock()
						}()

					}()
				}
			}
			// 注釋掉自動 rtp，避免 bot 持續傳送
			// if strings.Contains(cleanString, "不在具有 [操作物權限] 的領地") {
			// 	client.Logger.Infof("領地沒有操作權限，重新執行 /rtp")
			// 	client.SendCommand("/rtp")
			// }
			if result := recvPMRegex.FindStringSubmatch(cleanString); result != nil {
				if slices.Contains(owners, result[1]) {
					args := strings.Split(result[2], " ")

					// 如果以 /pm 開頭，跳過 /pm 和機器人名稱
					if len(args) > 2 && args[0] == "/pm" {
						args = args[2:]
					}

					switch args[0] {
					case "cmd":
						client.SendCommand(strings.Join(args[1:], " "))
					case "chat":
						client.SendText(strings.Join(args[1:], " "))
					case "water":
						if waterFillCancel != nil {
							waterFillCancel()
						}
						ctx, cancel := context.WithCancel(context.Background())
						waterFillCancel = cancel
						waterFillRunning = true

						if len(args) == 1 {
							go runAutoStairWater(ctx, client)
							break
						}
						if len(args) >= 7 {
							go runWaterFill(ctx, client, args[1:])
						}
					}
				}
			}

			if result := recvTeleportRegex.FindStringSubmatch(cleanString); result != nil {
				if slices.Contains(owners, result[1]) {
					client.SendCommand("/tpaccept " + result[1])
				}
			}

			if result := recvTeleportHereRegex.FindStringSubmatch(cleanString); result != nil {
				if slices.Contains(owners, result[1]) {
					client.SendCommand("/tok")
				}
			}

			return nil
		},
	})

	go runAutoStairWaterWhenReady(client)

	ticker := time.NewTicker(10 * time.Minute)
	go func() {
		if len(ads) == 0 {
			return
		}
		for {
			for _, ad := range ads {
				<-ticker.C
				if strings.HasPrefix(ad, "/") { // If the ad starts with a slash, it's a command
					client.SendCommand(ad) // Send a command packet every 10 minutes
				} else {
					client.SendText(ad) // Send a text packet every 10 minutes
				}
			}
		}
	}()

	err = client.HandleGame()
	if err != nil {
		panic(err)
	}
}

func runAutoStairWater(ctx context.Context, client *bot.Client) {
	defer func() {
		waterFillRunning = false
	}()

	for {
		pos := bot.BlockPosFromVec3(client.Self.Position)
		client.Logger.Infof("開始搜尋鵝卵石階梯，中心=%v，範圍=64", pos)

		err := client.AutoFillCobblestoneStairs(ctx, pos, 64, 3)
		if err == nil {
			client.Logger.Infof("本批 10 格自動填水完成，重新搜尋下一批")
			if !waitForRetry(ctx, time.Second) {
				return
			}
			continue
		}
		if ctx.Err() != nil {
			client.Logger.Infof("自動填水被中斷")
			return
		}
		client.Logger.Infof("自動填水失敗: %v，15 秒後重新搜尋", err)
		if !waitForRetry(ctx, 15*time.Second) {
			return
		}
	}
}

func waitForRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func runAutoStairWaterWhenReady(client *bot.Client) {
	for client.Self == nil || client.World() == nil {
		time.Sleep(500 * time.Millisecond)
	}
	time.Sleep(3 * time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	waterFillCancel = cancel
	waterFillRunning = true
	runAutoStairWater(ctx, client)
}

func runWaterFill(ctx context.Context, client *bot.Client, args []string) {
	defer func() {
		waterFillRunning = false
	}()

	if len(args) < 6 {
		client.Logger.Infof("填水參數不足")
		return
	}

	minX, minY, minZ, maxX, maxY, maxZ := 0, 0, 0, 0, 0, 0
	_, err := fmt.Sscanf(strings.Join(args[0:3], " "), "%d %d %d", &minX, &minY, &minZ)
	if err != nil {
		client.Logger.Infof("解析坐標失敗: %v", err)
		return
	}

	_, err = fmt.Sscanf(strings.Join(args[3:6], " "), "%d %d %d", &maxX, &maxY, &maxZ)
	if err != nil {
		client.Logger.Infof("解析坐標失敗: %v", err)
		return
	}

	cfg := bot.FishTowerFillConfig{
		MinX: minX, MaxX: maxX,
		MinY: minY, MaxY: maxY,
		MinZ: minZ, MaxZ: maxZ,
		Rows:     10,
		GapEvery: 2,
	}

	positions := bot.GenerateFishTowerFillPositions(cfg)
	client.Logger.Infof("開始填水，共 %d 個位置", len(positions))

	err = client.FillWaterBlocks(ctx, positions)
	if err != nil {
		if ctx.Err() != nil {
			client.Logger.Infof("填水被中斷")
		} else {
			client.Logger.Infof("填水失敗: %v", err)
		}
	} else {
		client.Logger.Infof("填水完成")
	}
}
