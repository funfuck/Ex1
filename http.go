package main

import (
	"log"
	"net/http"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
	"github.com/gorilla/mux"
	"encoding/json"
	"github.com/dgrijalva/jwt-go"
	"github.com/garyburd/redigo/redis"
	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/mysql"
	"time"
)

type Person struct {
	ID        	bson.ObjectId `bson:"_id,omitempty"`
	FirstName      	string
	LastName	string
	Email 		string
	Password 	string
}

type Response struct {
	Success bool
	Desc string
}

type ResponseMember struct{
	Success bool
	Desc string
	Result interface{}
}

func getJsonBody(r *http.Request) *Person{
	decoder := json.NewDecoder(r.Body)
	var p Person
	err := decoder.Decode(&p)
	if err != nil {
		log.Fatal(err)
	}
	return &p
}

func connectDb() *mgo.Session{
	session, err := mgo.Dial("127.0.0.1:27017")
	if err != nil {
		log.Fatal(err)
	}
	log.Println("connect to MongoDB")
	return session
}

func connectRedis() redis.Conn{
	c, err := redis.Dial("tcp", ":6379")
	if err != nil {
		log.Fatal(err)
	}
	log.Println("connect to Redis")
	return c
}

func response(w http.ResponseWriter, res *Response){
	uj, _ := json.Marshal(res)
	w.Write(uj)
}

func register(w http.ResponseWriter, r *http.Request) {

	p := getJsonBody(r)

	// db session
	session := connectDb()
	defer session.Close()

	// find document
	c := session.DB("test").C("person")

	result := Person{}
	err := c.Find(bson.M{"email": p.Email}).One(&result)
	if err != nil {
		log.Println(err)
	}

	// response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)

	if(result.Email == p.Email){
		response(w, &Response{Success:false, Desc:"Fail"})
	} else {
		err = c.Insert(&p)
		if err != nil {
			log.Fatal(err)
		}

		response(w, &Response{Success:true, Desc:"OK"})
	}
}

func login(w http.ResponseWriter, r *http.Request) {

	p := getJsonBody(r)

	session := connectDb()
	defer session.Close()

	// find document by email & password
	c := session.DB("test").C("person")
	count, err := c.Find(bson.M{"email": p.Email, "password": p.Password}).Count()
	if err != nil {
		log.Println(err)
	}

	// response
	w.Header().Set("Content-Type", "application/json")
	//w.WriteHeader(200)

	if count == 1 {
		var member = Person{}
		c.Find(bson.M{"email": p.Email, "password": p.Password}).One(&member)

		// create token
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"ID": member.ID})
		tokenString, err := token.SignedString([]byte("AllYourBase"))
		if err != nil {
			log.Println(err)
		}

		// save token to redis
		c := connectRedis()
		defer c.Close()

		jsonMember, _ := json.Marshal(member)
		c.Do("SET", tokenString, jsonMember)
		c.Do("EXPIRE", tokenString, 60)

		log.Println("tokenString : ", tokenString)

		w.Header().Add("token", tokenString)
		response(w, &Response{Success:true, Desc:"OK"})
	} else {
		response(w, &Response{Success:false, Desc:"Fail"})
	}
}

type MemberHistory struct {
	ID        	uint `gorm:"primary_key"`
	FirstName      	string
	LastName	string
	Email 		string
	TimeStamp	time.Time `gorm:"timestamp"`
}

func getRequestToken(r *http.Request) string{
	vars := mux.Vars(r)
	return vars["token"]

}

func getRedisToken(c redis.Conn, token string) string{
	member, err := redis.String(c.Do("GET", token))
	if err != nil {
		log.Println(err)
	}
	return member
}

func connectMysql() *gorm.DB {
	db, err := gorm.Open("mysql", "root:@/test")
	if err != nil {
		log.Fatal("failed to connect database")
	}
	log.Println("connect to Mysql")
	return db
}

func getMember(w http.ResponseWriter, r *http.Request) {

	// get token from request
	token := getRequestToken(r)

	// token expired or exist
	c := connectRedis()
	member := getRedisToken(c, token)

	// response {“success”:true|false,”desc”:”xxx”,result:{“firstName”:”xxx”,”lastName”:”xxx”}}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)

	if member != "" {
		var m Person
		json.Unmarshal([]byte(member), &m)
		responseMember,_ := json.Marshal(ResponseMember{Success:true, Desc:"OK", Result:m})
		w.Write(responseMember)
	} else {
		responseMember,_ := json.Marshal(ResponseMember{Success:false, Desc:"Fail", Result:nil})
		w.Write(responseMember)
	}
}

func updateMember(w http.ResponseWriter, r *http.Request){

	// get token from request
	token := getRequestToken(r)
	log.Println(token)

	// get member from Redis
	c := connectRedis()
	defer c.Close()
	member := getRedisToken(c, token)

	// member struct
	var m Person
	json.Unmarshal([]byte(member), &m)

	// response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)

	if member != "" {

		p := getJsonBody(r)

		// save history(in redis) to mysql
		db := connectMysql()
		defer db.Close()

		db.AutoMigrate(&MemberHistory{})
		db.Create(&MemberHistory{FirstName:m.FirstName, LastName:m.LastName, Email:m.Email, TimeStamp:time.Now()})

		// save to mongodb
		session := connectDb()
		defer session.Close()

		mgo := session.DB("test").C("person")

		doc := bson.M{"_id": m.ID}
		change := bson.M{"$set": bson.M{"firstname": p.FirstName, "lastname": p.LastName}}

		err := mgo.Update(doc, change)
		if err != nil {
			log.Fatal(err)
		}

		// save to Redis
		m.FirstName = p.FirstName
		m.LastName = p.LastName
		jsonMember, _ := json.Marshal(m)

		c.Do("SET", token, jsonMember)
		c.Do("EXPIRE", token, 60)

		// response
		responseMember,_ := json.Marshal(ResponseMember{Success:true, Desc:"OK", Result:m})
		w.Write(responseMember)
	} else {
		// response
		responseMember,_ := json.Marshal(ResponseMember{Success:false, Desc:"Fail", Result:nil})
		w.Write(responseMember)
	}
}

func main() {

	router := mux.NewRouter().StrictSlash(true)
	router.HandleFunc("/v1/member/register", register).Methods(http.MethodPost)
	router.HandleFunc("/v1/member/login", login).Methods(http.MethodPost)
	router.HandleFunc("/v1/member/{token}", getMember).Methods(http.MethodGet)
	router.HandleFunc("/v1/member/{token}", updateMember).Methods(http.MethodPut)

	log.Fatal(http.ListenAndServe(":8080", router))
}
