package main

import (
	"flag"
	"github.com/cihub/seelog"
	"github.com/claudiu/gocron"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/wangsongyan/wblog/controllers"
	"github.com/wangsongyan/wblog/helpers"
	"github.com/wangsongyan/wblog/models"
	"github.com/wangsongyan/wblog/system"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
)

func main() {

	// 定义命令行参数，指定配置文件路径、日志配置文件路径
	configFilePath := flag.String("C", "conf/conf.toml", "config file path")  // 容器内的配置文件路径
	logConfigPath := flag.String("L", "conf/seelog.xml", "log config file path")
	generate := flag.Bool("g", false, "generate sample config file")
	flag.Parse()

	if *generate {
		system.Generate()
		os.Exit(0)
	}

	// 日志初始化
	logger, err := seelog.LoggerFromConfigAsFile(*logConfigPath)
	if err != nil {
		seelog.Critical("err parsing seelog config file", err)
		return
	}
	seelog.ReplaceLogger(logger)
	defer seelog.Flush()

	if err := system.LoadConfiguration(*configFilePath); err != nil {
		seelog.Critical("err parsing config log file", err)
		return
	}

	// 数据库初始化
	db, err := models.InitDB()
	if err != nil {
		seelog.Critical("err open databases", err)
		return
	}
	defer func() {
		dbInstance, _ := db.DB()
		_ = dbInstance.Close()
	}()

	// ---Gin部分---
	gin.SetMode(gin.ReleaseMode)
	router := gin.Default()

	setTemplate(router)  // 模板设置
	setSessions(router)  // 会话设置
	router.Use(SharedData())  // 添加中间件，用于在每个请求中提供公共数据

	//定时任务，每天生成XML站点地图，每周备份数据
	gocron.Every(1).Day().Do(controllers.CreateXMLSitemap)
	gocron.Every(7).Days().Do(controllers.Backup)
	gocron.Start()

	// 静态文件服务，提供/static路径下的静态资源
	router.Static("/static", filepath.Join(helpers.GetCurrentDirectory(), system.GetConfiguration().PublicDir))

	router.NoRoute(controllers.Handle404)  // 404页面的处理函数
	router.GET("/", controllers.IndexGet)
	router.GET("/index", controllers.IndexGet)
	router.GET("/rss", controllers.RssGet)

	if system.GetConfiguration().SignupEnabled {
		router.GET("/signup", controllers.SignupGet)
		router.POST("/signup", controllers.SignupPost)
	}
	// 用户注册、登录、登出、OAuth认证相关路由
	router.GET("/signin", controllers.SigninGet)
	router.POST("/signin", controllers.SigninPost)
	router.GET("/logout", controllers.LogoutGet)
	router.GET("/oauth2callback", controllers.Oauth2Callback)
	router.GET("/auth/:authType", controllers.AuthGet)

	// 验证码路由
	router.GET("/captcha", controllers.CaptchaGet)

	// 设置访客可以访问的路由
	visitor := router.Group("/visitor")
	visitor.Use(AuthRequired(false))
	{
		visitor.POST("/new_comment", controllers.CommentPost)
		visitor.POST("/comment/:id/delete", controllers.CommentDelete)
	}

	// 订阅相关路由
	router.GET("/subscribe", controllers.SubscribeGet)
	router.POST("/subscribe", controllers.Subscribe)
	router.GET("/active", controllers.ActiveSubscriber)
	router.GET("/unsubscribe", controllers.UnSubscribe)

	router.GET("/page/:id", controllers.PageGet)
	router.GET("/post/:id", controllers.PostGet)
	router.GET("/tag/:tag", controllers.TagGet)
	router.GET("/archives/:year/:month", controllers.ArchiveGet)

	router.GET("/link/:id", controllers.LinkGet)

	// 后台管理路由
	authorized := router.Group("/admin")  // 分组路由，登陆后分组成/admin
	authorized.Use(AuthRequired(true))  // 校验管理身份
	{
		// index主页
		authorized.GET("/index", controllers.AdminIndex)

		// image upload
		authorized.POST("/upload", controllers.Upload)

		// page页面管理
		authorized.GET("/page", controllers.PageIndex)
		authorized.GET("/new_page", controllers.PageNew)
		authorized.POST("/new_page", controllers.PageCreate)
		authorized.GET("/page/:id/edit", controllers.PageEdit)
		authorized.POST("/page/:id/edit", controllers.PageUpdate)
		authorized.POST("/page/:id/publish", controllers.PagePublish)
		authorized.POST("/page/:id/delete", controllers.PageDelete)

		// post博文管理
		authorized.GET("/post", controllers.PostIndex)
		authorized.GET("/new_post", controllers.PostNew)
		authorized.POST("/new_post", controllers.PostCreate)
		authorized.GET("/post/:id/edit", controllers.PostEdit)
		authorized.POST("/post/:id/edit", controllers.PostUpdate)
		authorized.POST("/post/:id/publish", controllers.PostPublish)
		authorized.POST("/post/:id/delete", controllers.PostDelete)

		// tag
		authorized.POST("/new_tag", controllers.TagCreate)

		//
		authorized.GET("/user", controllers.UserIndex)
		authorized.POST("/user/:id/lock", controllers.UserLock)

		// profile
		authorized.GET("/profile", controllers.ProfileGet)
		authorized.POST("/profile", controllers.ProfileUpdate)
		authorized.POST("/profile/email/bind", controllers.BindEmail)
		authorized.POST("/profile/email/unbind", controllers.UnbindEmail)
		authorized.POST("/profile/github/unbind", controllers.UnbindGithub)

		// subscriber
		authorized.GET("/subscriber", controllers.SubscriberIndex)
		authorized.POST("/subscriber", controllers.SubscriberPost)

		// link
		authorized.GET("/link", controllers.LinkIndex)
		authorized.POST("/new_link", controllers.LinkCreate)
		authorized.POST("/link/:id/edit", controllers.LinkUpdate)
		authorized.POST("/link/:id/delete", controllers.LinkDelete)

		// comment
		authorized.POST("/comment/:id", controllers.CommentRead)
		authorized.POST("/read_all", controllers.CommentReadAll)

		// backup
		authorized.POST("/backup", controllers.BackupPost)
		authorized.POST("/restore", controllers.RestorePost)

		// mail
		authorized.POST("/new_mail", controllers.SendMail)
		authorized.POST("/new_batchmail", controllers.SendBatchMail)
	}

	err = router.Run(system.GetConfiguration().Addr)
	if err != nil {
		seelog.Critical(err)
	}
}

// 设置模板引擎和自定义模板函数
func setTemplate(engine *gin.Engine) {
	// 把模板里要用到的函数名 映射到 后端真实的Go函数上
	funcMap := template.FuncMap{
		"dateFormat": helpers.DateFormat,
		"substring":  helpers.Substring,
		"isOdd":      helpers.IsOdd,
		"isEven":     helpers.IsEven,
		"truncate":   helpers.Truncate,
		"length":     helpers.Len,
		"add":        helpers.Add,
		"minus":      helpers.Minus,
		"listtag":    helpers.ListTag,
	}

	engine.SetFuncMap(funcMap)  // 注册给Gin的模板引擎
	// 加载模板引擎
	engine.LoadHTMLGlob(filepath.Join(helpers.GetCurrentDirectory(), system.GetConfiguration().ViewDir))
}

// 初始化会话管理，使用cookie存储会话数据
func setSessions(router *gin.Engine) {
	config := system.GetConfiguration()
	store := cookie.NewStore([]byte(config.SessionSecret))
	// 创建Cookie存储的会话仓库 gin-contrib/session/cookie
	store.Options(sessions.Options{HttpOnly: true, MaxAge: 7 * 86400, Path: "/"}) //Also set Secure: true if using SSL, you should though
	router.Use(sessions.Sessions("gin-session", store))  // 把store放进cookie
}

//+++++++++++++ middlewares +++++++++++++++++++++++

// 共享数据中间件，在每个请求中提供公共数据
func SharedData() gin.HandlerFunc {
	return func(c *gin.Context) {
		session := sessions.Default(c)
		if uID := session.Get(controllers.SessionKey); uID != nil {
			user, err := models.GetUser(uID)
			if err == nil {
				c.Set(controllers.ContextUserKey, user)
			}
		}
		if system.GetConfiguration().SignupEnabled {
			c.Set("SignupEnabled", true)
		}
		c.Next()
	}
}

// 后台管理身份认证中间件
func AuthRequired(adminScope bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if user, _ := c.Get(controllers.ContextUserKey); user != nil {  // 从context中取出用户
			if u, ok := user.(*models.User); ok && (!adminScope || u.IsAdmin) {
				c.Next()
				return
			}
		}
		seelog.Warnf("User not authorized to visit %s", c.Request.RequestURI)
		c.HTML(http.StatusForbidden, "errors/error.html", gin.H{
			"message": "Forbidden!",
		})
		c.Abort()
	}
}
