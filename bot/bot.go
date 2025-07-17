package bot

//go:generate sqlboiler --no-hooks psql

import (
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/botlabs-gg/yagpdb/v2/bot/eventsystem"
	"github.com/botlabs-gg/yagpdb/v2/bot/shardmemberfetcher"
	"github.com/botlabs-gg/yagpdb/v2/common"
	"github.com/botlabs-gg/yagpdb/v2/common/config"
	"github.com/botlabs-gg/yagpdb/v2/common/pubsub"
	"github.com/botlabs-gg/yagpdb/v2/lib/discordgo"
	"github.com/botlabs-gg/yagpdb/v2/lib/dshardorchestrator/node"
	"github.com/botlabs-gg/yagpdb/v2/lib/dstate"
	"github.com/botlabs-gg/yagpdb/v2/lib/dstate/inmemorytracker"
	dshardmanager "github.com/botlabs-gg/yagpdb/v2/lib/jdshardmanager"
	"github.com/mediocregopher/radix/v3"
)

var (
	// When the bot was started
	Started      = time.Now()
	Enabled      bool // wether the bot is set to run at some point in this process
	Running      bool // wether the bot is currently running
	State        dstate.StateTracker
	stateTracker *inmemorytracker.InMemoryTracker

	ShardManager *dshardmanager.Manager

	NodeConn          *node.Conn
	UsingOrchestrator bool
)

var (
	confConnEventChannel         = config.RegisterOption("yagpdb.connevt.channel", "Gateway connection logging channel", 0)
	confConnStatus               = config.RegisterOption("yagpdb.connstatus.channel", "Gateway connection status channel", 0)
	confShardOrchestratorAddress = config.RegisterOption("yagpdb.orchestrator.address", "Sharding orchestrator address to connect to, if set it will be put into orchstration mode", "")

	confFixedShardingConfig = config.RegisterOption("yagpdb.sharding.fixed_config", "Fixed sharding config, mostly used during testing, allows you to run a single shard, the format is: 'id,count', example: '0,10'", "")

	usingFixedSharding bool
	fixedShardingID    int

	// Note yags is using priviledged intents
	gatewayIntentsUsed = []discordgo.GatewayIntent{
		discordgo.GatewayIntentGuilds,
		discordgo.GatewayIntentGuildMembers,
		discordgo.GatewayIntentGuildModeration,
		discordgo.GatewayIntentGuildExpressions,
		discordgo.GatewayIntentGuildVoiceStates,
		discordgo.GatewayIntentGuildPresences,
		discordgo.GatewayIntentGuildMessages,
		discordgo.GatewayIntentGuildMessageReactions,
		discordgo.GatewayIntentDirectMessages,
		discordgo.GatewayIntentDirectMessageReactions,
		discordgo.GatewayIntentMessageContent,
		discordgo.GatewayIntentGuildScheduledEvents,
		discordgo.GatewayIntentAutomoderationExecution,
		discordgo.GatewayIntentAutomoderationConfiguration,
	}
)

var (
	// the total amount of shards this bot is set to use across all processes
	totalShardCount int
)

// Run intializes and starts the discord bot component of yagpdb
func Run(nodeID string) {
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("[bot.Run] PANIC RECOVERED: %v", r)
		}
		logger.Info("[bot.Run] END OF FUNCTION REACHED")
	}()

	setup()

	logger.Println("Running bot")

	// either start standalone or set up a connection to a shard orchestrator
	orcheStratorAddress := confShardOrchestratorAddress.GetString()
	if orcheStratorAddress != "" {
		UsingOrchestrator = true
		logger.Infof("Set to use orchestrator at address: %s", orcheStratorAddress)
	} else {
		logger.Info("Running standalone without any orchestrator")
		setupStandalone()
	}

	go mergedMessageSender()

	Running = true

	if UsingOrchestrator {
		NodeConn = node.NewNodeConn(&NodeImpl{}, orcheStratorAddress, common.VERSION, nodeID, nil)
		NodeConn.Run()
	} else {
		ShardManager.Init()
		if usingFixedSharding {
			go ShardManager.Session(fixedShardingID).Open()
		} else {
			go ShardManager.Start()
		}
		botReady()
	}

	logger.Info("[bot.Run] BOTTOM OF FUNCTION (should always see this unless os.Exit or panic)")
}

func setup() {
	common.InitSchemas("core_bot", DBSchema)

	discordgo.IdentifyRatelimiter = &identifyRatelimiter{}

	addBotHandlers()
	setupShardManager()
}

func setupStandalone() {
  logger.Info("[setupStandalone] INICIO")

  logger.Info("[setupStandalone] Antes de confFixedShardingConfig.GetString()")
  if confFixedShardingConfig.GetString() == "" {
	  logger.Info("[setupStandalone] Antes de ShardManager.GetRecommendedCount()")
	  shardCount, err := ShardManager.GetRecommendedCount()
	  logger.Info("[setupStandalone] Después de ShardManager.GetRecommendedCount()")
	  if err != nil {
	   panic("Failed getting shard count: " + err.Error())
	  }
	  totalShardCount = shardCount
  } else {
	  logger.Info("[setupStandalone] Antes de readFixedShardingConfig()")
	  fixedShardingID, totalShardCount = readFixedShardingConfig()
	  logger.Info("[setupStandalone] Después de readFixedShardingConfig()")
	  usingFixedSharding = true
	  logger.Info("[setupStandalone] Antes de ShardManager.SetNumShards()")
	  ShardManager.SetNumShards(totalShardCount)
	  logger.Info("[setupStandalone] Después de ShardManager.SetNumShards()")
  }

  logger.Info("[setupStandalone] Antes de setupState()")
  setupState()
  logger.Info("[setupStandalone] Después de setupState()")

  logger.Infof("[setupStandalone] Antes de EventLogger.init(%d)", totalShardCount)
  EventLogger.init(totalShardCount)
  logger.Info("[setupStandalone] Después de EventLogger.init()")

  logger.Infof("[setupStandalone] Antes de eventsystem.InitWorkers(%d)", totalShardCount)
  eventsystem.InitWorkers(totalShardCount)
  logger.Info("[setupStandalone] Después de eventsystem.InitWorkers()")

  logger.Infof("[setupStandalone] Antes de ReadyTracker.initTotalShardCount(%d)", totalShardCount)
  ReadyTracker.initTotalShardCount(totalShardCount)
  logger.Info("[setupStandalone] Después de ReadyTracker.initTotalShardCount()")

  logger.Info("[setupStandalone] Antes de lanzar goroutine EventLogger.run()")
  go EventLogger.run()
  logger.Info("[setupStandalone] Después de lanzar goroutine EventLogger.run()")

  logger.Info("[setupStandalone] Antes de ReadyTracker.shardsAdded loop")
  for i := 0; i < totalShardCount; i++ {
	  ReadyTracker.shardsAdded(i)
  }
  logger.Info("[setupStandalone] Después de ReadyTracker.shardsAdded loop")

  logger.Infof("[setupStandalone] Antes de RedisPool.Do SET yagpdb_total_shards = %d", totalShardCount)
  err := common.RedisPool.Do(radix.FlatCmd(nil, "SET", "yagpdb_total_shards", totalShardCount))
  if err != nil {
	  logger.WithError(err).Error("failed setting shard count")
  } else {
	  logger.Info("[setupStandalone] SET yagpdb_total_shards OK")
  }

  logger.Info("[setupStandalone] FIN")
}

func readFixedShardingConfig() (id int, count int) {
	conf := confFixedShardingConfig.GetString()
	if conf == "" {
		return 0, 0
	}

	split := strings.SplitN(conf, ",", 2)
	if len(split) < 2 {
		panic("Invalid yagpdb.sharding.fixed_config: " + conf)
	}

	parsedID, err := strconv.ParseInt(split[0], 10, 64)
	if err != nil {
		panic("Invalid yagpdb.sharding.fixed_config: " + err.Error())
	}

	parsedCount, err := strconv.ParseInt(split[1], 10, 64)
	if err != nil {
		panic("Invalid yagpdb.sharding.fixed_config: " + err.Error())
	}

	return int(parsedID), int(parsedCount)
}

// called when the bot is ready and the shards have started connecting
func botReady() {
	logger.Info("[botReady] START")

	pubsub.AddHandler("bot_status_changed", func(evt *pubsub.Event) {
		updateAllShardStatuses()
	}, nil)
	logger.Info("[botReady] After pubsub.AddHandler")

	memberFetcher = shardmemberfetcher.NewManager(int64(totalShardCount), State, func(guildID int64, userIDs []int64, nonce string) error {
		shardID := guildShardID(guildID)
		session := ShardManager.Session(shardID)
		if session != nil {
			session.GatewayManager.RequestGuildMembersComplex(&discordgo.RequestGuildMembersData{
				GuildID:   guildID,
				Presences: false,
				UserIDs:   userIDs,
				Nonce:     nonce,
			})
		} else {
			return errors.New("session not found")
		}

		return nil
	}, ReadyTracker)
	logger.Info("[botReady] After memberFetcher init")

	serviceDetails := "Not using orchestrator"
	if UsingOrchestrator {
		serviceDetails = "Using orchestrator, NodeID: " + common.NodeID
	}

	// register us with the service discovery
	common.ServiceTracker.RegisterService(common.ServiceTypeBot, "Bot", serviceDetails, botServiceDetailsF)
	logger.Info("[botReady] After ServiceTracker.RegisterService")

	// Initialize all plugins
	for _, plugin := range common.Plugins {
		if initBot, ok := plugin.(BotInitHandler); ok {
			pi := plugin.PluginInfo()
			logger.Infof("[botReady] Calling BotInit for plugin: %s", pi.Name)
			initBot.BotInit()
			logger.Infof("[botReady] Finished BotInit for plugin: %s", pi.Name)
		}
	}
	logger.Info("[botReady] After BotInitHandler loop")

	// Initialize all plugins late
	for _, plugin := range common.Plugins {
		if initBot, ok := plugin.(LateBotInitHandler); ok {
			initBot.LateBotInit()
		}
	}
	logger.Info("[botReady] After LateBotInitHandler loop")

	go runUpdateMetrics()
	go loopCheckAdmins()
	logger.Info("[botReady] After starting runUpdateMetrics and loopCheckAdmins goroutines")

	watchMemusage()
	logger.Info("[botReady] END")
}

var stopOnce sync.Once

func StopAllPlugins(wg *sync.WaitGroup) {
	stopOnce.Do(func() {
		for _, v := range common.Plugins {
			stopper, ok := v.(BotStopperHandler)
			if !ok {
				continue
			}
			wg.Add(1)
			logger.Debug("Calling bot stopper for: ", v.PluginInfo().Name)
			go stopper.StopBot(wg)
		}

		close(stopRunCheckAdmins)
	})
}

func Stop(wg *sync.WaitGroup) {
	StopAllPlugins(wg)
	ShardManager.StopAll()
	wg.Done()
}

func GuildCountsFunc() []int {
	numShards := ShardManager.GetNumShards()
	result := make([]int, numShards)

	for i := 0; i < numShards; i++ {
		guilds := State.GetShardGuilds(int64(i))
		result[i] = len(guilds)
	}

	return result
}

type identifyRatelimiter struct {
	mu                   sync.Mutex
	lastShardRatelimited int
	lastRatelimitAt      time.Time
}

func (rl *identifyRatelimiter) RatelimitIdentify(shardID int) {
	const key = "yagpdb.gateway.identify.limit"
	for {

		if rl.checkSameBucket(shardID) {
			return
		}

		// The ratelimit is 1 identify every 5 seconds, but with exactly that limit we will still encounter invalid session
		// closes, probably due to small variances in networking and scheduling latencies
		// Adding a extra 100ms fixes this completely, but to be on the safe side we add a extra 50ms
		var resp string
		err := common.RedisPool.Do(radix.Cmd(&resp, "SET", key, "1", "PX", "5100", "NX"))
		if err != nil {
			logger.WithError(err).Error("failed ratelimiting gateway")
			time.Sleep(time.Second)
			continue
		}

		if resp == "OK" {
			// We ackquired the lock, our turn to identify now
			rl.mu.Lock()
			rl.lastShardRatelimited = shardID
			rl.lastRatelimitAt = time.Now()
			rl.mu.Unlock()
			return
		}

		// otherwise a identify was sent by someone else last 5 seconds
		time.Sleep(time.Second)
	}
}

func (rl *identifyRatelimiter) checkSameBucket(shardID int) bool {
	if !common.ConfLargeBotShardingEnabled.GetBool() {
		// only works with large bot sharding enabled
		return false
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	if rl.lastRatelimitAt.IsZero() {
		return false
	}

	// normally 16, but thats a bit too fast for us, so we use 4
	bucketSize := common.ConfShardBucketSize.GetInt()
	currentBucket := shardID / bucketSize
	lastBucket := rl.lastShardRatelimited / bucketSize

	if currentBucket != lastBucket {
		return false
	}

	if time.Since(rl.lastRatelimitAt) > time.Second*5 {
		return false
	}

	// same large bot sharding bucket
	return true
}

// var (
// 	metricsCacheHits = promauto.NewCounter(prometheus.CounterOpts{
// 		Name: "yagpdb_state_cache_hits_total",
// 		Help: "Cache hits in the satte cache",
// 	})

// 	metricsCacheMisses = promauto.NewCounter(prometheus.CounterOpts{
// 		Name: "yagpdb_state_cache_misses_total",
// 		Help: "Cache misses in the sate cache",
// 	})

// 	metricsCacheEvictions = promauto.NewCounter(prometheus.CounterOpts{
// 		Name: "yagpdb_state_cache_evicted_total",
// 		Help: "Cache evictions",
// 	})

// 	metricsCacheMemberEvictions = promauto.NewCounter(prometheus.CounterOpts{
// 		Name: "yagpdb_state_members_evicted_total",
// 		Help: "Members evicted from state cache",
// 	})
// )

var confStateRemoveOfflineMembers = config.RegisterOption("yagpdb.state.remove_offline_members", "Remove offline members from state", true)

// func setupState() {
// 	// Things may rely on state being available at this point for initialization
// 	State = dstate.NewState()
// 	State.MaxChannelMessages = 1000
// 	State.MaxMessageAge = time.Hour
// 	// State.Debug = true
// 	State.ThrowAwayDMMessages = true
// 	State.TrackPrivateChannels = false
// 	State.CacheExpirey = time.Hour * 2

// 	if confStateRemoveOfflineMembers.GetBool() {
// 		State.RemoveOfflineMembers = true
// 	}

// 	go State.RunGCWorker()

// 	eventsystem.DiscordState = State

// 	// track cache hits/misses
// 	go func() {
// 		lastHits := int64(0)
// 		lastMisses := int64(0)
// 		lastEvictionsCache := int64(0)
// 		lastEvictionsMembers := int64(0)

// 		ticker := time.NewTicker(time.Minute)
// 		for {
// 			<-ticker.C

// 			stats := State.StateStats()
// 			deltaHits := stats.CacheHits - lastHits
// 			deltaMisses := stats.CacheMisses - lastMisses
// 			lastHits = stats.CacheHits
// 			lastMisses = stats.CacheMisses

// 			metricsCacheHits.Add(float64(deltaHits))
// 			metricsCacheMisses.Add(float64(deltaMisses))

// 			metricsCacheEvictions.Add(float64(stats.UserCachceEvictedTotal - lastEvictionsCache))
// 			metricsCacheMemberEvictions.Add(float64(stats.MembersRemovedTotal - lastEvictionsMembers))

// 			lastEvictionsCache = stats.UserCachceEvictedTotal
// 			lastEvictionsMembers = stats.MembersRemovedTotal

// 			// logger.Debugf("guild cache Hits: %d Misses: %d", deltaHits, deltaMisses)
// 		}
// 	}()
// }

var StateLimitsF func(guildID int64) (int, time.Duration) = func(guildID int64) (int, time.Duration) {
	return 1000, time.Hour
}

func setupState() {

	removeMembersDur := time.Duration(0)
	if confStateRemoveOfflineMembers.GetBool() {
		removeMembersDur = time.Hour
	}

	tracker := inmemorytracker.NewInMemoryTracker(inmemorytracker.TrackerConfig{
		ChannelMessageLimitsF:     StateLimitsF,
		RemoveOfflineMembersAfter: removeMembersDur,
		BotMemberID:               common.BotUser.ID,
	}, int64(totalShardCount))

	go tracker.RunGCLoop(time.Second)

	eventsystem.DiscordState = tracker

	stateTracker = tracker
	State = tracker
}

func setupShardManager() {
	connEvtChannel := confConnEventChannel.GetInt()
	connStatusChannel := confConnStatus.GetInt()

	// Set up shard manager
	ShardManager = dshardmanager.New(common.GetBotToken())
	ShardManager.LogChannel = int64(connEvtChannel)
	ShardManager.StatusMessageChannel = int64(connStatusChannel)
	ShardManager.Name = "YAGPDB"
	ShardManager.GuildCountsFunc = GuildCountsFunc
	ShardManager.SessionFunc = func(token string) (session *discordgo.Session, err error) {
		session, err = discordgo.New(token)
		if err != nil {
			return
		}

		session.StateEnabled = false
		session.LogLevel = discordgo.LogInformational
		session.SyncEvents = true
		session.Intents = gatewayIntentsUsed

		// Certain discordgo internals expect this to be present
		// but in case of shard migration it's not, so manually assign it here
		session.State.Ready = discordgo.Ready{
			User: &discordgo.SelfUser{
				User: common.BotUser,
			},
		}

		return
	}

	// Only handler
	ShardManager.AddHandler(eventsystem.HandleEvent)
}

func botServiceDetailsF() (details *common.BotServiceDetails, err error) {
	if !UsingOrchestrator {
		totalShards := ShardManager.GetNumShards()
		shards := make([]int, totalShards)
		for i := 0; i < totalShards; i++ {
			shards[i] = i
		}

		return &common.BotServiceDetails{
			OrchestratorMode: false,
			TotalShards:      totalShards,
			RunningShards:    shards,
		}, nil
	}

	totalShards := getTotalShards()
	running := ReadyTracker.GetProcessShards()

	return &common.BotServiceDetails{
		TotalShards:      int(totalShards),
		RunningShards:    running,
		NodeID:           common.NodeID,
		OrchestratorMode: true,
	}, nil
}
