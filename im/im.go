/**
 * Copyright (c) 2014-2015, GoBelieve     
 * All rights reserved.
 *
 * This program is free software; you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation; either version 2 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program; if not, write to the Free Software
 * Foundation, Inc., 59 Temple Place, Suite 330, Boston, MA  02111-1307  USA
 */

package main
import "time"
import "sync/atomic"
import "github.com/valyala/gorpc"
import log "github.com/golang/glog"

//个人消息／普通群消息／客服消息
func GetStorageRPCClient(uid int64) *gorpc.DispatcherClient {
	if uid < 0 {
		uid = -uid
	}
	index := uid%int64(len(rpc_clients))
	return rpc_clients[index]
}

//超级群消息
func GetGroupStorageRPCClient(group_id int64) *gorpc.DispatcherClient {
	if group_id < 0 {
		group_id = -group_id
	}
	index := group_id%int64(len(group_rpc_clients))
	return group_rpc_clients[index]
}

func GetChannel(uid int64) *Channel{
	if uid < 0 {
		uid = -uid
	}
	index := uid%int64(len(route_channels))
	return route_channels[index]
}

func GetGroupChannel(group_id int64) *Channel{
	if group_id < 0 {
		group_id = -group_id
	}
	index := group_id%int64(len(group_route_channels))
	return group_route_channels[index]
}

func GetRoomChannel(room_id int64) *Channel {
	if room_id < 0 {
		room_id = -room_id
	}
	index := room_id%int64(len(route_channels))
	return route_channels[index]
}

func GetGroupMessageDeliver(group_id int64) *GroupMessageDeliver {
	if group_id < 0 {
		group_id = -group_id
	}
	
	deliver_index := atomic.AddUint64(&current_deliver_index, 1)
	index := deliver_index%uint64(len(group_message_delivers))
	return group_message_delivers[index]
}

func SaveGroupMessage(appid int64, gid int64, device_id int64, msg *Message) (int64, error) {
	dc := GetGroupStorageRPCClient(gid)
	
	gm := &GroupMessage{
		AppID:appid,
		GroupID:gid,
		DeviceID:device_id,
		Cmd:int32(msg.cmd),
		Raw:msg.ToData(),
	}
	resp, err := dc.Call("SaveGroupMessage", gm)
	if err != nil {
		log.Warning("save group message err:", err)
		return 0, err
	}
	msgid := resp.(int64)
	log.Infof("save group message:%d %d %d\n", appid, gid, msgid)
	return msgid, nil
}

func SaveMessage(appid int64, uid int64, device_id int64, m *Message) (int64, error) {
	dc := GetStorageRPCClient(uid)
	
	pm := &PeerMessage{
		AppID:appid,
		Uid:uid,
		DeviceID:device_id,
		Cmd:int32(m.cmd),
		Raw:m.ToData(),
	}

	resp, err := dc.Call("SavePeerMessage", pm)
	if err != nil {
		log.Error("save peer message err:", err)
		return 0, err
	}

	msgid := resp.(int64)
	log.Infof("save peer message:%d %d %d %d\n", appid, uid, device_id, msgid)
	return msgid, nil
}

//群消息通知(apns, gcm...)
func PushGroupMessage(appid int64, group *Group, m *Message) {
	channels := make(map[*Channel][]int64)
	members := group.Members()
	for member := range members {
		//不对自身推送
		if im, ok := m.body.(*IMMessage); ok {
			if im.sender == member {
				continue
			}
		}
		channel := GetChannel(member)
		if _, ok := channels[channel]; !ok {
			channels[channel] = []int64{member}
		} else {
			receivers := channels[channel]
			receivers = append(receivers, member)
			channels[channel] = receivers
		}
	}

	for channel, receivers := range channels {
		channel.Push(appid, receivers, m)
	}
}

//离线消息推送
func PushMessage(appid int64, uid int64, m *Message) {	
	channel := GetChannel(uid)
	channel.Push(appid, []int64{uid}, m)
}

func PublishMessage(appid int64, uid int64, m *Message) {
	now := time.Now().UnixNano()
	amsg := &AppMessage{appid:appid, receiver:uid, msgid:0, timestamp:now, msg:m}
	channel := GetChannel(uid)
	channel.Publish(amsg)
}

func PublishGroupMessage(appid int64, group_id int64, msg *Message) {
	now := time.Now().UnixNano()
	amsg := &AppMessage{appid:appid, receiver:group_id, msgid:0, timestamp:now, msg:msg}
	channel := GetGroupChannel(group_id)
	channel.PublishGroup(amsg)
}

func SendAppGroupMessage(appid int64, group_id int64, msg *Message) {
	now := time.Now().UnixNano()
	amsg := &AppMessage{appid:appid, receiver:group_id, msgid:0, timestamp:now, msg:msg}
	channel := GetGroupChannel(group_id)
	channel.PublishGroup(amsg)
	DispatchGroupMessage(amsg)
}

func SendAppMessage(appid int64, uid int64, msg *Message) {
	now := time.Now().UnixNano()
	amsg := &AppMessage{appid:appid, receiver:uid, msgid:0, timestamp:now, msg:msg}
	channel := GetChannel(uid)
	channel.Publish(amsg)
	DispatchAppMessage(amsg)
}

func DispatchAppMessage(amsg *AppMessage) {
	now := time.Now().UnixNano()
	d := now - amsg.timestamp
	log.Infof("dispatch app message:%s %d %d", Command(amsg.msg.cmd), amsg.msg.flag, d)
	if d > int64(time.Second) {
		log.Warning("dispatch app message slow...")
	}

	route := app_route.FindRoute(amsg.appid)
	if route == nil {
		log.Warningf("can't dispatch app message, appid:%d uid:%d cmd:%s", amsg.appid, amsg.receiver, Command(amsg.msg.cmd))
		return
	}
	clients := route.FindClientSet(amsg.receiver)
	if len(clients) == 0 {
		log.Infof("can't dispatch app message, appid:%d uid:%d cmd:%s", amsg.appid, amsg.receiver, Command(amsg.msg.cmd))
		return
	}
	for c, _ := range(clients) {
		c.EnqueueNonBlockMessage(amsg.msg)
	}
}

func DispatchRoomMessage(amsg *AppMessage) {
	log.Info("dispatch room message", Command(amsg.msg.cmd))
	room_id := amsg.receiver
	route := app_route.FindOrAddRoute(amsg.appid)
	clients := route.FindRoomClientSet(room_id)

	if len(clients) == 0 {
		log.Infof("can't dispatch room message, appid:%d room id:%d cmd:%s", amsg.appid, amsg.receiver, Command(amsg.msg.cmd))
		return
	}
	for c, _ := range(clients) {
		c.EnqueueNonBlockMessage(amsg.msg)
	}	
}

func DispatchGroupMessage(amsg *AppMessage) {
	now := time.Now().UnixNano()
	d := now - amsg.timestamp
	log.Infof("dispatch group message:%s %d %d", Command(amsg.msg.cmd), amsg.msg.flag, d)
	if d > int64(time.Second) {
		log.Warning("dispatch group message slow...")
	}

	deliver := GetGroupMessageDeliver(amsg.receiver)
	deliver.DispatchMessage(amsg)
}

func DispatchMessageToGroup(amsg *AppMessage, group *Group) {
	if group == nil {
		log.Warningf("can't dispatch group message, appid:%d group id:%d", amsg.appid, amsg.receiver)
		return
	}

	route := app_route.FindRoute(amsg.appid)
	if route == nil {
		log.Warningf("can't dispatch app message, appid:%d uid:%d cmd:%s", amsg.appid, amsg.receiver, Command(amsg.msg.cmd))
		return
	}

	members := group.Members()
	for member := range members {
	    clients := route.FindClientSet(member)
		if len(clients) == 0 {
			continue
		}

		for c, _ := range(clients) {
			c.EnqueueNonBlockMessage(amsg.msg)
		}
	}
}

