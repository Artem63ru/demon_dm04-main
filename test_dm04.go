package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/goburrow/modbus"
	"github.com/tbrandon/mbserver"
)

const Version = "0.21"

var config tomlConfig                          // структура конфигуратора
var Log *log.Logger                            // это логер для записи
var Debug bool                                 // флаг отладки на экране
var CoilsRed [65536]CoilsRedirect              // массив структур для редиректа записи  Coils на устройства
var HRegRed [65536]HoldingRegisterRedirect     // массив структур для редиректа записи HoldenRegister
var InDiscreteStatus [65536]InputDiscrteStatus //
var InputRegStatus [65536]InputRegistersStatus // статусы обмена Input Registers
/*
Версии:
	0.1 - тестированите Modbus Serial RTU (функция 04), в одиночном serial канале
	0.2 - задание порта обмена (-p) и скорости обмена (-b), флагами в командной строке
	0.3 - введение циклитческого опроса и остановки опроса по Ctrl+C
	0.4 - реализация нескольких комманд опроса Modbus RTU
	0.5 - описываем реализацию slave на serial канале
	0.6 - налало работы компилятора под Windows
	0.7 - Добавил файйл логов
	0.8 - попытка сделать опрос устройства с конфигурацией из toml файа (первого канала в списке и первой ноды)
	0.9 - введение проверок конфигурации из toml файла
	0.10 - добавил сервер для запросов верхнего уровня
	0.11 - оперирую с массивами переменных сервера, coils, input registers, holding registerc
	0.12 - открытие каналов опроса перенес по подпрограмму req
	0.13 - в конфигурации все настройки цифры сделал в uint
	0.14 - сделал правильную обработку дискретов и сохранение на сервере, на сервере массив байт и состояние 0 или 1
	0.15 - добавление записи Coils и HoldenREgisters
	0.16 - работа с симуляторами serial каналов на Linux и Windows (tcp и так одинаковые)
	0.17 - все бьюсь с записью Coils и HoldenREgisters и добавление каналорв по TCP. Команды уже проходят до обработчиков
	0.18 - реализация записи Coils и Holding Registers (одиночное точно работает)
	0.19 - добавляем каналы по TCP
	0.20 - расширенная обработка ошибок при запросах
	0.21 - наполняем обработку ошибок
*/

type CoilsRedirect struct {
	ChanelSerial int            // канал для записи Coils с работой по Serial, 0-если не используется
	ChanelTCP    int            // канал для задписи Coils с работой по TCP или UDP
	Address_id   uint8          // адрес устройства на шине
	Address_data uint16         // адрес Coils В устройстве
	icc          chan<- inc_req // канал для передачи данных записи в гороутину
	Time         time.Time      // время последней записи в Coils
	Status       uint8          // статус записи еденичного Ciolsa устройства
}
type KR_registrs_cils struct {
	KR_num       int            // номер крана/канала для управления
	ChanelTCP    int            // канал для задписи Coils с работой по TCP или UDP
	Address_id   uint8          // адрес устройства на шине
	sesion       int            // Открытие ссесии 0-нет 1-открыта
	pred         int            // предварительная команда 0-нет 1-открыть 2-закрыть
	TU           int            // команда 0-нет 1-открыть 2-закрыть
	Address_data uint16         // адрес Coils В устройстве начальный
	icc          chan<- inc_req // канал для передачи данных записи в гороутину
	Time         time.Time      // время последней записи в Coils
	Status       uint8          // статус записи еденичного Ciolsa устройства
}

type HoldingRegisterRedirect struct {
	ChanelSerial int            // канал для записи Coils с работой по Serial, 0-если не используется
	ChanelTCP    int            // канал для задписи Coils с работой по TCP или UDP
	Address_id   uint8          // адрес устройства на шине
	Address_data uint16         // адрес Coils В устройстве
	icc          chan<- inc_req // какал для передачи данных записи в гороутину
	Time         time.Time      // время последней записи в Coils
	Status       uint8          // статус записи еденичного Ciolsa устройства
}

// структура для описания работы каналов ввода input distrete
type InputDiscrteStatus struct {
	EnableInputDiscrete bool      // true - у нас используется адрес для запросов input discrete
	Time                time.Time // время последнего обращения
	Status              uint8     // статус устройства
}

// структура для описания работы каналов ввода input registers
type InputRegistersStatus struct {
	EnableInputRegisters bool      // true - у нас используется адрес для запросов input discrete
	Time                 time.Time // время последнего обращения
	Status               uint8     // статус устройства
}

// структура конфигурации
type tomlConfig struct {
	Version          string       // версия
	Upport_tcp       uint16       // порт для запросов верзнего уровня
	Local_port       uint16       // локальный порт для работы с управляющей программой
	Tty_serial       []set_serial // миссив настроек для устройств по tty
	Tcp_serial       []set_tcp    // массив каналов по TCP
	Count_tty_serial int          // количество используемых каналов tty
	Count_tcp_serial int          // еолизество используемых каналов tcp
}

// описание канала ро TCP
type set_tcp struct {
	Ip         string // IP адрес канала
	Port       int    // порт для реализации сервера
	Count_node uint   // количество устройств на порту
	Set_node   []node // это массив нод
	Time_loop  uint   // время цикла опроса master между нодами
}

// описание ноды на канале TCP
type node struct {
	Address_id   uint8  // адрес на шине
	Enable       bool   // включено устройство в опрос или нет
	Command      uint8  // обрабатываемая комманда опроса
	Address_data uint16 // адрес начала данных с ноде
	Data_length  uint16 // длинна данных
	Index_up     uint   // позиция данных с ноды в на глобальной карте параметров
	Type_par     uint   // Тип параметра получаемого от такт-у 1-AI, 2-DI
	// изменяется во время опроса по ответу-неответу от устройства - делать не здесь, а в статусе
	//	Time   time.Time // время последнего опроса
	//	Status uint8     // статус опроса устройства

}

type chanel struct {
	Enable      bool // true - включено / false - выключено
	Type_chanel int  // тип канала
}

// настройки канала для всех нод однотипные
type set_serial struct {
	Port_tty string // последовательный порт
	Baud     uint   // скорость обменеа
	Stop     uint8  // количество стопов
	Bits     uint8  // количество бит
	Parity   string // паритет "N"-нет,"E"-event,"O"-odd
	Slave    bool   // канал slave: true - slave - выдача запросов из макипоранныой катру параметров
	// если true - то параметры ниже не работают
	Count_node uint   // количество устройств на порту
	Set_node   []node // это массив нод
	Time_loop  uint   // время цикла опроса master между нодами
}

// ***************************************************************************************
// проверка на корректность прараметров конфигурации, считанной из toml файла
// возвращает true, если успешно
func config_control() bool {

	return true
}

// ***************************************************************************************
// Проверка на ошибку для уменьшения писанины
func err_log(err3 error, result []byte) {
	if err3 != nil {
		Log.Printf("**ERROR** Chanel: %s", result)
		os.Exit(-1)
	}
}

// структура для передачи запроса на исполнение команды с данными в канал с необходимым нам устройством Modbus RTU
type inc_req struct {
	Address_id   uint8       // адрес устройства на Modbus RTU
	Command      uint8       // команда для исполнения по Modbus RTU (5,6,15,16 - WriteSingle(Multiple)Coils/HoldingRegisters)
	Address_data uint16      // адрес данных в устройстве
	Data_length  uint16      // длинна запрашиваемых-передаваемых данных
	Data         [125]uint16 // данные для операции (максимальня длинна)
	Pos          uint16      // позиция первого параметра в карте памяти Coils или HildingRegisters для записи Time и Status последней операции
}

// ***************************************************************************************
// запрос необходимых данных от устройства
var kr KR_registrs_cils

// ***************************************************************************************
// запрос необходимых данных от устройства
// chanel - указатель на структуру канала опроса устройств
// sever - сервер modbus для записи данныых опроса устройств
func req_serial(server *mbserver.Server, chanel *set_serial, cc <-chan struct{}, inc <-chan inc_req) {
	var xx inc_req
	handler := modbus.NewRTUClientHandler(chanel.Port_tty) // с версии 0.17 пишем порты напямую, можно COM-порты пускать под Wndows

	//	handler.RS485.Enabled = true
	handler.RS485.Enabled = false // не может быть true - драйвер для allwinner H3 не может это сделать, а ch344 делает это аппаратно
	handler.BaudRate = int(chanel.Baud)
	//	handler.DataBits = 8
	handler.DataBits = int(chanel.Bits)
	//	handler.Parity = "N"
	handler.Parity = chanel.Parity
	//	handler.StopBits = 1
	handler.StopBits = int(chanel.Stop)
	handler.SlaveId = 1
	handler.Timeout = time.Millisecond * time.Duration(200)
	err := handler.Connect()
	defer handler.Close()
	if err != nil {
		// пока нет расширеннной обработки ошибки запроса - просто выход
		if Debug {
			fmt.Printf(err.Error())
		}
		Log.Printf("**ERROR** REQ: No open Serial Port chanel: '%s'\r\n", chanel.Port_tty)
		Log.Printf(err.Error())
		os.Exit(-1) // критическая ошибка !!!!!
		//return // просто выходим из подпрограмы опроса канала
	}

	client := modbus.NewClient(handler)

	for {
		// пока обработка только одной ноды оз конфигурации
		for count := 0; count < int(chanel.Count_node); count++ {
			if handler.SlaveId != chanel.Set_node[count].Address_id {
				// надо опробывать без закрытия handler
				//				handler.Close()
				handler.SlaveId = chanel.Set_node[count].Address_id
				//				handler.Connect()
				//				defer handler.Close()
				client = modbus.NewClient(handler) // перезапустим клиента
			}
			switch chanel.Set_node[count].Command {
			// *************************************
			case 04:
				result4, err2 := client.ReadInputRegisters(uint16(chanel.Set_node[count].Address_data), uint16(chanel.Set_node[count].Data_length))
				if err2 != nil {
					// пока нет расширеннной обработки ошибки запроса - просто выход - пора расширять
					Log.Printf("**ERROR** Read Input Register Chanel: %s\n", chanel.Port_tty)
					Log.Printf(err2.Error())
					if Debug {
						fmt.Printf(err2.Error())
					}
					InputRegStatus[chanel.Set_node[count].Index_up].Status = 0x10     // ошибка, чегото там, пока не декодируем
					InputRegStatus[chanel.Set_node[count].Index_up].Time = time.Now() // время последней ошибки

					//					os.Exit(-1) // пока выходим, не делаем обработку
				} else {
					if Debug {
						fmt.Printf("Chanel: %s,\tRead Input Regires(4):\t\tnode %d: %v\n", chanel.Port_tty, count, result4)
					}
					// можно сохранить в памяти сервера
					//					_, err := client_local.WriteMultipleRegisters(uint16(chanel.Set_node[count].Index_up), uint16(chanel.Set_node[count].Data_length), result4)
					new_data := mbserver.BytesToUint16(result4)
					for ii := 0; ii < int(chanel.Set_node[count].Data_length); ii++ {
						server.InputRegisters[ii+int(chanel.Set_node[count].Index_up)] = new_data[ii]
					}
				}
				// *************************************
			case 01:
				result1, err2 := client.ReadCoils(uint16(chanel.Set_node[count].Address_data), uint16(chanel.Set_node[count].Data_length))
				if err2 != nil {
					// пока нет расширеннной обработки ошибки запроса - просто выход !!!!!
					Log.Printf("**ERROR** Read Coils Chanel: %s", chanel.Port_tty)
					Log.Printf(err2.Error())
					fmt.Printf(err2.Error())
					os.Exit(-1)
				} else {
					if Debug {
						fmt.Printf("Chanel: %s,\tRead Coils(1):\t\t\tnode %d: %v\n", chanel.Port_tty, count, result1)
					}
					// можно сохранить в памяти сервера
					//					_, err := client_local.WriteMultipleCoils(uint16(chanel.Set_node[count].Index_up), uint16(chanel.Set_node[count].Data_length), result2)
					new_data := result1
					for i := 0; i < len(new_data); i++ {
						for b := 0; b < 8; b++ {
							if (b + i*8) < int(chanel.Set_node[count].Data_length) {
								if (new_data[i] & byte(1<<b)) != 0 {
									server.Coils[i*8+b+int(chanel.Set_node[count].Index_up)] = 1
								} else {
									server.Coils[i*8+b+int(chanel.Set_node[count].Index_up)] = 0
								}
							} else {
								goto OutLoop2
							}
						}
					}
				OutLoop2:
				}
				// *************************************
			case 02:
				result2, err2 := client.ReadDiscreteInputs(uint16(chanel.Set_node[count].Address_data), uint16(chanel.Set_node[count].Data_length))
				if err2 != nil {
					// пока нет расширеннной обработки ошибки запроса - просто выход !!!!!
					Log.Printf("**ERROR** Read Input Discrete Chanel: %s", chanel.Port_tty)
					Log.Printf(err2.Error())
					fmt.Printf(err2.Error())
					os.Exit(-1)
				} else {
					if Debug {
						fmt.Printf("Chanel: %s,\tRead Input Discretes(2):\tnode %d: %v\n", chanel.Port_tty, count, result2)
					}
					// можно сохранить в памяти сервера
					//	_, err := client_local.WriteMultipleCoils(uint16(chanel.Set_node[count].Index_up), uint16(chanel.Set_node[count].Data_length), result2)
					new_data := result2
					/*	for ii := 0; ii < int(chanel.Set_node[count].Data_length); ii++ {
						//	server.DiscreteInputs[ii+int(chanel.Set_node[count].Index_up)] = new_data[ii]
							server.DiscreteInputs[ii+int(chanel.Set_node[count].Index_up)] = result2[ii]
						}
					*/
					for i := 0; i < len(new_data); i++ {
						for b := 0; b < 8; b++ {
							if (b + i*8) < int(chanel.Set_node[count].Data_length) {
								if (new_data[i] & byte(1<<b)) != 0 {
									server.DiscreteInputs[i*8+b+int(chanel.Set_node[count].Index_up)] = 1
								} else {
									server.DiscreteInputs[i*8+b+int(chanel.Set_node[count].Index_up)] = 0
								}
							} else {
								goto OutLoop1
							}
						}
					}
				OutLoop1:
				}
				// *************************************

			case 03:
				result3, err2 := client.ReadHoldingRegisters(uint16(chanel.Set_node[count].Address_data), uint16(chanel.Set_node[count].Data_length))
				if err2 != nil {
					// пока нет расширеннной обработки ошибки запроса - просто выход !!!!!
					Log.Printf("**ERROR** Read Holding Register Chanel: %s", chanel.Port_tty)
					Log.Printf(err2.Error())
					fmt.Printf(err2.Error())
					os.Exit(-1)
				} else {
					if Debug {
						//						fmt.Printf(": node%d: %v\n", count, resulTimet3)
						fmt.Printf("Chanel: %s,\tRead Holding Registers(3):\tnode %d: %v\n", chanel.Port_tty, count, result3)
					}
					// можно сохранить в памяти сервера
					//	_, err := client_local.WriteMultipleRegisters(uint16(chanel.Set_node[count].Index_up), uint16(chanel.Set_node[count].Data_length), result3)
					new_data := mbserver.BytesToUint16(result3)
					for ii := 0; ii < int(chanel.Set_node[count].Data_length); ii++ {
						server.HoldingRegisters[ii+int(chanel.Set_node[count].Index_up)] = new_data[ii]
					}
					// if err != nil {
					//	fmt.Printf(err.Error())
					//	}
				}
			default:
				// пока ошибки toml на уровне описания ноды не обрабатываю
				Log.Printf("**ERROR**, Chanel %s, error to toml file, code Command in node %d\r\n", chanel.Port_tty, count)
				os.Exit(-1) // ошибка критическия надо править toml !!!!!
			}
			// time.Sleep(time.Millisecond * time.Duration(config.Tty_serial[0].Time_loop)) // время цикла запроса передать из config
			select {
			case xx = <-inc: // принмаем данные на обработку
				if Debug {
					fmt.Printf("\t----- Execute COMMAND: '%d' for chanel '%s'\r\n", xx.Command, chanel.Port_tty)
				}
				// можно  разбирать, что выдавать и куда
				switch xx.Command {
				case 05: // Write Single Coils
					if handler.SlaveId != xx.Address_id {
						//						handler.Close()
						handler.SlaveId = xx.Address_id
						//						handler.Connect()
						//						defer handler.Close()
						client = modbus.NewClient(handler) // перезапустим клиента
					}
					write5, err := client.WriteSingleCoil(xx.Address_data, xx.Data[0])
					CoilsRed[xx.Pos].Time = time.Now() // последнее время записи в Coils
					if err != nil {
						// ошибка записи Single Copils
						Log.Printf("**ERROR** Write Single Coils, Chanel: %s AddrID: %d\r\n", chanel.Port_tty, xx.Address_id)
						Log.Printf("**data Responce: %v", write5)
						CoilsRed[xx.Pos].Status = 2 // ошибка записи в !!!!!
					}
				case 06: // Write Single Holding Regster
					if handler.SlaveId != xx.Address_id {
						//						handler.Close()
						handler.SlaveId = xx.Address_id
						//						handler.Connect()
						//						defer handler.Close()
						client = modbus.NewClient(handler) // перезапустим клиента
					}
					write6, err := client.WriteSingleRegister(xx.Address_data, xx.Data[0])
					HRegRed[xx.Pos].Time = time.Now() // последнее время записи в Coils
					if err != nil {
						// ошибка записи Single Holding REgister
						Log.Printf("**ERROR** Write Single Holding Register, Chanel: %s AddrID: %d\r\n", chanel.Port_tty, xx.Address_id)
						Log.Printf("**data Responce: %v", write6)
						HRegRed[xx.Pos].Status = 2 // ошибка записи - надо ввести   типизацию кодов !!!!!
					} else {

					}
				case 15: // Write  Multiple Coils
				case 16: // Write Multiple Holding Regster
				default: // непонятная команда пришла в канал
				}
			default:
			}
		} //for
		// неблокирующий прием данных из канала
		select {
		case <-cc: // получили сигнал на завершение
			break
		default: // обязательно для получения неблокирующий функции
			time.Sleep(time.Millisecond * time.Duration(config.Tty_serial[0].Time_loop)) // время цикла запроса передать из config
		}
	}
}

//func (s *Server) RegisterFunctionHandler(funcCode uint8, function func(*Server, Framer) ([]byte, *Exception)

// ***************************************************************************************
// функция обработки записи еденичного Coil (5)
func WrSingleCoilsOverr(server *mbserver.Server) {
	var xx KR_registrs_cils
	server.RegisterFunctionHandler(5,
		func(s *mbserver.Server, frame mbserver.Framer) ([]byte, *mbserver.Exception) {
			data1 := frame.GetData()
			register := int(binary.BigEndian.Uint16(data1[0:2])) // адрес регистра
			numRegs := int(binary.BigEndian.Uint16(data1[2:4]))  // это как раз данные для записи 0xFF00 - on, 0x0000 - off
			xx.Address_id = CoilsRed[register].Address_id        // адрес устройства на шине modbus
			xx.Address_data = CoilsRed[register].Address_data    // адрес данных в устройстве
			xx.Time = time.Now()
			if numRegs > 0 {
				s.Coils[register] = 1
				switch register {
				// *************************************
				case int(config.Tcp_serial[0].Set_node[5].Index_up):
					fmt.Printf("\t>>>>> Сеанс управление закрыт: %v\r\n", uint8(register))
					kr.sesion = 1
				case int(config.Tcp_serial[0].Set_node[5].Index_up - 6):
					fmt.Printf("\t>>>>> Предварительная закрыть: %v\r\n", uint8(register))
					kr.pred = 2
				case int(config.Tcp_serial[0].Set_node[5].Index_up - 5):
					fmt.Printf("\t>>>>> Управление краном закрыть: %v\r\n", uint8(register))
					kr.TU = 2
				}

			} else {
				s.Coils[register] = 0
				switch register {
				// *************************************
				case int(config.Tcp_serial[0].Set_node[5].Index_up):
					fmt.Printf("\t>>>>> Сеанс управление открыт: %v\r\n", uint8(register))
					kr.sesion = 0
				case int(config.Tcp_serial[0].Set_node[5].Index_up - 6):
					fmt.Printf("\t>>>>> Предварительная открыть: %v\r\n", uint8(register))
					kr.pred = 1
				case int(config.Tcp_serial[0].Set_node[5].Index_up - 5):
					fmt.Printf("\t>>>>> Управление краном открыть: %v\r\n", uint8(register))
					kr.TU = 1
				}
			}
			return data1, &mbserver.Success
		})
}

// ***************************************************************************************
// функция обработки записи цепочки Coilы (15)
func WrMultipleCoilsOverr(server *mbserver.Server) {
	server.RegisterFunctionHandler(15,
		func(s *mbserver.Server, frame mbserver.Framer) ([]byte, *mbserver.Exception) {
			data1 := frame.GetData()
			register := int(binary.BigEndian.Uint16(data1[0:2]))
			numRegs := int(binary.BigEndian.Uint16(data1[2:4]))
			//			endRegister := register + numRegs
			//		register, numRegs, endRegister := frame.registerAddressAndNumber()
			data := make([]byte, 4)
			data[0] = CoilsRed[register].Address_id
			data[1] = 5 // запись еденичного Coils
			data[2] = byte(CoilsRed[register].Address_data)
			if Debug {
				fmt.Printf("\t>>>>> Write Multiple Coils: %v\r\n", data1)
				//				fmt.Printf("register: %d, numRegs: 0x%2F, endRegister %d\r\n", register, numRegs, endRegister)
				h := fmt.Sprintf("%04x", numRegs)
				fmt.Printf("\t>>>>> register: %d, data: 0x%s\r\n", register, h)
			}
			/*
				dataSize := numRegs / 8
				data := make([]byte, 1+dataSize)
				data[0] = byte(dataSize)
				for i := range s.DiscreteInputs[register:endRegister] {
					// Return all 1s, regardless of the value in the DiscreteInputs array.
					shift := uint(i) % 8
					data[1+i/8] |= byte(1 << shift)
				}
			*/
			return data1, &mbserver.Success
		})
}

// ***************************************************************************************
// функция обработки записи еденичного Register (6)
func WrSingleRegisterOverr(server *mbserver.Server) {
	var xx inc_req
	server.RegisterFunctionHandler(6,
		func(s *mbserver.Server, frame mbserver.Framer) ([]byte, *mbserver.Exception) {
			data1 := frame.GetData()
			register := int(binary.BigEndian.Uint16(data1[0:2]))
			numRegs := int(binary.BigEndian.Uint16(data1[2:4]))
			endRegister := register + numRegs
			//		register, numRegs, endRegister := frame.registerAddressAndNumber()
			xx.Address_id = HRegRed[register].Address_id     // адрес устройства на щине modbus
			xx.Command = 6                                   // запись еденичного Holding Register
			xx.Address_data = HRegRed[register].Address_data // адрес данных в устройстве
			xx.Pos = uint16(register)                        // позиция в табюлице реверса для записи статусов
			xx.Data[0] = uint16(numRegs)                     // данные по записи
			xx.Data_length = 1                               // у нас один регистр
			HRegRed[register].icc <- xx                      // перезадим данные в нужную нам горутину

			if Debug {
				fmt.Printf("\t>>>>> Write Single Registers: %v\r\n", data1)
				fmt.Printf("\t>>>>> register: %d, numRegs: %d, endRegister %d\r\n", register, numRegs, endRegister)
			}
			//			нeужен свой расклад, что, куда писать из массива CoilsRed

			//			dataSize := numRegs / 8
			//			data := make([]byte, 1+dataSize)
			//			data[0] = byte(dataSize)
			//			for i := range s.DiscreteInputs[register:endRegister] {
			//				// Return all 1s, regardless of the value in the DiscreteInputs array.
			//				shift := uint(i) % 8
			//				data[1+i/8] |= byte(1 << shift)
			//			}
			return data1, &mbserver.Success
		})
}

// ***************************************************************************************
// функция обработки записи цепочки Registers (16)
func WrMultipleRegisterOverr(server *mbserver.Server) {
	server.RegisterFunctionHandler(16,
		func(s *mbserver.Server, frame mbserver.Framer) ([]byte, *mbserver.Exception) {
			data1 := frame.GetData()
			register := int(binary.BigEndian.Uint16(data1[0:2]))
			numRegs := int(binary.BigEndian.Uint16(data1[2:4]))
			endRegister := register + numRegs
			//		register, numRegs, endRegister := frame.registerAddressAndNumber()
			if endRegister > 65535 { // слишком большай адрес
				return []byte{}, &mbserver.IllegalDataAddress
			}
			data := make([]byte, 4)
			data[0] = CoilsRed[register].Address_id
			data[1] = 5 // запись еденичного Coils
			data[2] = byte(CoilsRed[register].Address_data)
			if Debug {
				fmt.Printf("\t>>>>> Write Multiple Hilding Registers: %v\r\n", data1)
				fmt.Printf("\t>>>>> register: %d, numRegs: %d, endRegister %d\r\n", register, numRegs, endRegister)
				//				h := fmt.Sprintf("%04x", numRegs)
				//				fmt.Printf("\t>>>>>register: %d, data: 0x%s\r\n", register, h)
			}
			/*
				dataSize := numRegs / 8
				data := make([]byte, 1+dataSize)
				data[0] = byte(dataSize)
				for i := range s.DiscreteInputs[register:endRegister] {
					// Return all 1s, regardless of the value in the DiscreteInputs array.
					shift := uint(i) % 8
					data[1+i/8] |= byte(1 << shift)
				}
			*/
			return data1, &mbserver.Success
		})
}

//****************************************************************************************
// функция управления кранами

// ***************************************************************************************
// з
func req_tcp_serial(server *mbserver.Server, chanel *set_tcp, cc <-chan struct{}, inc <-chan inc_req) {
	var xx inc_req
	//var timer1 time.Timer
	//	handler := modbus.NewRTUClientHandler(chanel.Port_tty) // с версии 0.17 пишем порты напямую, можно COM-порты пускать под Wndows
	handler := modbus.NewTCPClientHandler(chanel.Ip + ":" + strconv.Itoa(chanel.Port)) // с версии 0.17 пишем порты напямую, можно COM-порты пускать под Wndows
	handler.IdleTimeout = 10                                                           //  тайл-аут на операции
	handler.SlaveId = 1

	err := handler.Connect()
	defer handler.Close()

	if err != nil {
		// пока нет расширеннной обработки ошибки запроса - просто выход
		if Debug {
			fmt.Printf(err.Error())
		}
		Log.Printf("**ERROR** REQ: No open Serial Port chanel: '%s'\r\n", chanel.Ip)
		Log.Printf(err.Error())
		os.Exit(-1) // критическая ошибка !!!!!
		//return // просто выходим из подпрограмы опроса канала
	}

	client := modbus.NewClient(handler)

	for {
		// пока обработка только одной ноды оз конфигурации
		for count := 0; count < int(chanel.Count_node); count++ {
			if handler.SlaveId != chanel.Set_node[count].Address_id {
				// надо опробывать без закрытия handler
				//				handler.Close()
				handler.SlaveId = chanel.Set_node[count].Address_id
				//				handler.Connect()
				//				defer handler.Close()
				client = modbus.NewClient(handler) // перезапустим клиента
			}
			switch chanel.Set_node[count].Command {
			// *************************************
			case 04:
				result4, err2 := client.ReadInputRegisters(uint16(chanel.Set_node[count].Address_data), uint16(chanel.Set_node[count].Data_length))
				if err2 != nil {
					// пока нет расширеннной обработки ошибки запроса - просто выход - пора расширять
					Log.Printf("**ERROR** TCP Read Input Register Chanel: %s", chanel.Ip)
					Log.Printf(err2.Error())
					fmt.Printf(err2.Error())
					os.Exit(-1)
				} else {
					if Debug {
						fmt.Printf("Chanel: %s,\tRead Input Regires(4):\t\tnode %d: %v\n", chanel.Ip, count, result4)
					}
					// можно сохранить в памяти сервера
					//					_, err := client_local.WriteMultipleRegisters(uint16(chanel.Set_node[count].Index_up), uint16(chanel.Set_node[count].Data_length), result4)
					new_data := mbserver.BytesToUint16(result4)
					for ii := 0; ii < int(chanel.Set_node[count].Data_length); ii++ {
						server.InputRegisters[ii+int(chanel.Set_node[count].Index_up)] = new_data[ii]
					}
				}
				// *************************************
			case 01: // Для такт у не нужна
				result1, err2 := client.ReadCoils(uint16(chanel.Set_node[count].Address_data), uint16(chanel.Set_node[count].Data_length))
				if err2 != nil {
					// пока нет расширеннной обработки ошибки запроса - просто выход !!!!!
					Log.Printf("**ERROR** Read Coils Chanel: %s", chanel.Ip)
					Log.Printf(err2.Error())
					fmt.Printf(err2.Error())
					os.Exit(-1)
				} else {
					if Debug {
						fmt.Printf("Chanel: %s,\tRead Coils(1):\t\t\tnode %d: %v\n", chanel.Ip, count, result1)
					}
					// можно сохранить в памяти сервера
					//					_, err := client_local.WriteMultipleCoils(uint16(chanel.Set_node[count].Index_up), uint16(chanel.Set_node[count].Data_length), result2)
					new_data := result1
					for i := 0; i < len(new_data); i++ {
						for b := 0; b < 8; b++ {
							if (b + i*8) < int(chanel.Set_node[count].Data_length) {
								if (new_data[i] & byte(1<<b)) != 0 {
									server.Coils[i*8+b+int(chanel.Set_node[count].Index_up)] = 1
								} else {
									server.Coils[i*8+b+int(chanel.Set_node[count].Index_up)] = 0
								}
							} else {
								goto OutLoop2
							}
						}
					}
				OutLoop2:
				}
				// *************************************
			case 02:
				result2, err2 := client.ReadDiscreteInputs(uint16(chanel.Set_node[count].Address_data), uint16(chanel.Set_node[count].Data_length))
				if err2 != nil {
					// пока нет расширеннной обработки ошибки запроса - просто выход !!!!!
					Log.Printf("**ERROR** Read Input Discretes Chanel: %s", chanel.Ip)
					Log.Printf(err2.Error())
					fmt.Printf(err2.Error())
					os.Exit(-1)
				} else {
					if Debug {
						fmt.Printf("Chanel: %s,\tRead Input Discretes(2):\tnode %d: %v\n", chanel.Ip, count, result2)
					}
					// можно сохранить в памяти сервера
					//	_, err := client_local.WriteMultipleCoils(uint16(chanel.Set_node[count].Index_up), uint16(chanel.Set_node[count].Data_length), result2)
					new_data := result2
					for i := 0; i < len(new_data); i++ {
						for b := 0; b < 8; b++ {
							if (b + i*8) < int(chanel.Set_node[count].Data_length) {
								if (new_data[i] & byte(1<<b)) != 0 {
									server.DiscreteInputs[i*8+b+int(chanel.Set_node[count].Index_up)] = 1
								} else {
									server.DiscreteInputs[i*8+b+int(chanel.Set_node[count].Index_up)] = 0
								}
							} else {
								goto OutLoop1
							}
						}
					}
				OutLoop1:
				}
				// *************************************

			case 03:
				result3, err2 := client.ReadHoldingRegisters(uint16(chanel.Set_node[count].Address_data), uint16(chanel.Set_node[count].Data_length))
				if err2 != nil {
					// пока нет расширеннной обработки ошибки запроса - просто выход !!!!!
					Log.Printf("**ERROR** Read Holding Register - Chanel: %s", chanel.Ip)
					Log.Printf(err2.Error())
					fmt.Printf(err2.Error())
					os.Exit(-1)
				} else {
					if Debug {
						fmt.Printf("Chanel: %s,\tRead Holding Registers(3):\tnode %d: %v\n", chanel.Ip, count, result3)
					}
					// можно сохранить в памяти сервера
					//	_, err := client_local.WriteMultipleRegisters(uint16(chanel.Set_node[count].Index_up), uint16(chanel.Set_node[count].Data_length), result3)
					new_data := mbserver.BytesToUint16(result3)
					for ii := 0; ii < int(chanel.Set_node[count].Data_length); ii++ {
						if chanel.Set_node[count].Type_par == 1 { // Пишем регистр в область InputRegisters
							//server.HoldingRegisters[ii+int(chanel.Set_node[count].Index_up)] = new_data[ii]
							// Пересчет милиамперов из Такт-У в кода АЦП Зонда 0-4096
							s := int16(new_data[ii])
							var d = float32(0)
							if float32(s) >= float32(4000) {
								d = float32((float32(s)-4000)/16000) * 4095 // переводим в шкалу 0-4095/4мА - 20 мА
							}
							new_data[ii] = uint16(d)
							server.InputRegisters[ii+int(chanel.Set_node[count].Index_up)] = new_data[ii]
						}
						if chanel.Set_node[count].Type_par == 2 { // Разбираем Слово на биты если нам надо прочитать дискретный вход
							buf := new_data[ii]
							server.DiscreteInputs[int(chanel.Set_node[count].Index_up)-1] = 1     //СВЯЗЬ С устройством 0 - нет связи
							server.DiscreteInputs[int(chanel.Set_node[count].Index_up)-1+168] = 1 // Достоверность сдвигаем на область дискретов тут 168
							for j := 0; j < 16; j++ {
								if (buf>>j)&1 == 1 {
									server.DiscreteInputs[j+int(chanel.Set_node[count].Index_up)] = 1
									server.DiscreteInputs[j+int(chanel.Set_node[count].Index_up)+168] = 1 // Достоверность
								} else {
									server.DiscreteInputs[j+int(chanel.Set_node[count].Index_up)] = 0
									server.DiscreteInputs[j+int(chanel.Set_node[count].Index_up)+168] = 1 // Достоверность
								}

							}
						}
					}
				}
			case 05: // Write Single Coils
				if handler.SlaveId != 1 {
					//						handler.Close()
					handler.SlaveId = 1
					//						handler.Connect()
					//						defer handler.Close()
					client = modbus.NewClient(handler) // перезапустим клиента
				}
				server.Coils[chanel.Set_node[count].Index_up] = server.Coils[chanel.Set_node[count].Index_up]
				for ii := 0; ii < int(chanel.Set_node[count].Data_length); ii++ {
					// Если пришла команда закрытия и есть сеанс
					var Seans_KR_OFF = server.Coils[int(chanel.Set_node[count].Index_up)+ii] == 0 && kr.TU == 1 && !(server.DiscreteInputs[int(chanel.Set_node[count-1].Index_up)+4] == 1) // Если пришла команда закрытия и есть сеанс
					var Seans_KR_ON = server.Coils[int(chanel.Set_node[count].Index_up)+ii] == 0 && kr.TU == 2 && !(server.DiscreteInputs[int(chanel.Set_node[count-1].Index_up)+5] == 1)  // Если пришла команда открытия и есть сеанс
					if Seans_KR_OFF {
						result4, err3 := client.ReadHoldingRegisters(uint16(36), uint16(1)) // Вычитываем что в регистре управления DO
						err_log(err3, result4)
						if kr.pred != 2 { // Если отмена
							result5, err3 := client.WriteSingleRegister(uint16(chanel.Set_node[count].Address_data), binary.LittleEndian.Uint16(result4)|uint16(00000001<<1))
							err_log(err3, result5)
						} else {
							result5, err3 := client.WriteSingleRegister(uint16(chanel.Set_node[count].Address_data), binary.LittleEndian.Uint16(result4)^uint16(00000001<<1))
							err_log(err3, result5)
						}
					} else { // Если пришла команда открытия и есть сеанс
						if Seans_KR_ON {
							result4, err3 := client.ReadHoldingRegisters(uint16(36), uint16(1)) // Вычитываем что в регистре управления DO
							err_log(err3, result4)
							if kr.pred != 2 { // Если отмена
								result5, err3 := client.WriteSingleRegister(uint16(chanel.Set_node[count].Address_data), uint16(binary.LittleEndian.Uint16(result4)|uint16(00000001<<2)))
								err_log(err3, result5)
								//if timer1.Stop() {
								//	timer1 := time.NewTimer(10 * time.Second)
								//	<-timer1.C
								//	fmt.Printf("\t>>>>> Отработал таймер 10 сек: %v\r\n", timer1)
								//}

							} else {
								result5, err3 := client.WriteSingleRegister(uint16(chanel.Set_node[count].Address_data), uint16(binary.LittleEndian.Uint16(result4)^uint16(00000001<<2)))
								err_log(err3, result5)
							}
						}
						// Если сеанс закрыли сбрасываем в 00
						if !Seans_KR_ON && !Seans_KR_OFF {
							result4, err3 := client.ReadHoldingRegisters(uint16(36), uint16(1)) // Вычитываем что в регистре управления DO
							err_log(err3, result4)
							client.WriteSingleRegister(uint16(chanel.Set_node[count].Address_data), uint16(binary.LittleEndian.Uint16(result4)&uint16(00)))
							kr.TU = 0 //Сброс ТУ
						}
					}
					if err != nil {
						// ошибка записи Single Copils
						Log.Printf("**ERROR** Write Single Coils, Chanel: %s AddrID: %d\r\n", chanel.Ip, 1)
						//Log.Printf("**data Responce: %v", write5)
						CoilsRed[xx.Pos].Status = 2 // ошибка записи в !!!!!
					}
				}
			default:
				// пока ошибки toml на уровне описания ноды не обрабатываю
				Log.Printf("**ERROR**, Chanel %s, error to toml file, code Command in node %d\r\n", chanel.Ip, count)
				os.Exit(-1) // ошибка критическия надо править toml !!!!!
			}
			// time.Sleep(time.Millisecond * time.Duration(config.Tty_serial[0].Time_loop)) // время цикла запроса передать из config
			select {
			case xx = <-inc: // принмаем данные на обработку
				if Debug {
					fmt.Printf("\t----- Execute COMMAND: '%d' for chanel '%s'\r\n", xx.Command, chanel.Ip)
				}
				// можно  разбирать, что выдавать и куда
				switch xx.Command {
				case 05: // Write Single Coils
					if handler.SlaveId != xx.Address_id {
						//						handler.Close()
						handler.SlaveId = xx.Address_id
						//						handler.Connect()
						//						defer handler.Close()
						client = modbus.NewClient(handler) // перезапустим клиента
					}
					write5, err := client.WriteSingleCoil(xx.Address_data, xx.Data[0])
					CoilsRed[xx.Pos].Time = time.Now() // последнее время записи в Coils
					if err != nil {
						// ошибка записи Single Copils
						Log.Printf("**ERROR** Write Single Coils, Chanel: %s AddrID: %d\r\n", chanel.Ip, xx.Address_id)
						Log.Printf("**data Responce: %v", write5)
						CoilsRed[xx.Pos].Status = 2 // ошибка записи в !!!!!
					}
				case 06: // Write Single Holding Regster
					if handler.SlaveId != xx.Address_id {
						//						handler.Close()
						handler.SlaveId = xx.Address_id
						//						handler.Connect()
						//						defer handler.Close()
						client = modbus.NewClient(handler) // перезапустим клиента
					}
					write6, err := client.WriteSingleRegister(xx.Address_data, xx.Data[0])
					HRegRed[xx.Pos].Time = time.Now() // последнее время записи в Coils
					if err != nil {
						// ошибка записи Single Holding REgister
						Log.Printf("**ERROR** Write Single Holding Register, Chanel: %s AddrID: %d\r\n", chanel.Ip, xx.Address_id)
						Log.Printf("**data Responce: %v", write6)
						HRegRed[xx.Pos].Status = 2 // ошибка записи - надо ввести   типизацию кодов !!!!!
					} else {

					}
				case 15: // Write  Multiple Coils
				case 16: // Write Multiple Holding Regster
				default: // непонятная команда пришла в канал
				}
			default:
			}
		} //for
		// неблокирующий прием данных из канала
		select {
		case <-cc: // получили сигнал на завершение
			break
		default: // обязательно для получения неблокирующий функции
			time.Sleep(time.Millisecond * time.Duration(config.Tcp_serial[0].Time_loop)) // время цикла запроса передать из config
		}
	}
}

// ***************************************************************************************
// Головная функция, однако
func main() {
	//	var Port string
	//	var Baud int

	// пока есть вариант загрузки параметров из командной строки
	//	flag.StringVar(&Port, "p", "/dev/ttyS2", "communication port")
	//	flag.IntVar(&Baud, "b", 115200, "Baud rate")
	flag.BoolVar(&Debug, "d", false, "Debug print read parametrs")
	flag.Parse()

	// пытаемся открыть файл для записи лога
	fl, errl := os.OpenFile("test_dm04.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0777)
	if errl != nil {
		panic(errl.Error()) // proper error handling instead of panic in your app
	}
	defer fl.Close() // закрываем после выхода
	Log = log.New(fl, "", log.Ldate|log.Ltime|log.LUTC)

	fmt.Printf("START PROGRAM TEST DM04: v%s !!!!\r\n", Version)
	Log.Printf("\n\r\n\r****-----------------------------------------------------------****\n")
	Log.Printf("START PROGRAM TEST DM04: v%s !!!!\r\n", Version)

	// далее обработка toml файла конфигурации
	_, errt := toml.DecodeFile("test_dm04.toml", &config)
	if errt != nil {
		Log.Println("Eror load *.toml file")
		Log.Println(errt)
		os.Exit(-1)
	}
	if config_control() {
		Log.Printf("OK *.toml configuration file")
	} else {
		Log.Printf("ERROR *.toml configuration file")
		os.Exit(-1)
	}

	// ***********************************************************************************************
	// необходимо ввести проверку загруженных структур в config
	Log.Printf("config: %v\n", config)
	Log.Printf("version toml: %s\n", config.Version)

	// подключаем прием запросов от host Modbus TCP
	serv := mbserver.NewServer()

	// необходимо зарегистрировать обработчики команд 5 (WriteSingleCoils) и 6 (WriteSingle)
	WrSingleRegisterOverr(serv)
	WrMultipleRegisterOverr(serv)
	WrSingleCoilsOverr(serv)
	WrMultipleCoilsOverr(serv)

	err := serv.ListenTCP("0.0.0.0:" + strconv.Itoa(int(config.Upport_tcp)))
	if err != nil {
		log.Printf("Невозможно открыть на прослушку порт: %d - для связи c HOST\r\n", config.Upport_tcp)
		//		log.Printf("%v\n", err)
		os.Exit(-1)
	}
	// это для запросов от ПО на локальной машине
	err = serv.ListenTCP("127.0.0.1:" + strconv.Itoa(int(config.Local_port)))
	if err != nil {
		log.Printf("Невозможно открыть на прослушку порт: %d - для связи c LOCAL HOST\r\n", config.Local_port)
		//		log.Printf("%v\n", err)
		os.Exit(-1)
	}
	Log.Printf("OK load host server PORT: %d\n", config.Upport_tcp)
	Log.Printf("OK load local server PORT: %d\n\n", config.Local_port)

	defer serv.Close()

	for count := 0; count < int(config.Tcp_serial[0].Count_node); count++ {
		if config.Tcp_serial[0].Set_node[count].Type_par == 3 {
			kr.KR_num = 1
			kr.Address_data = uint16(config.Tcp_serial[0].Set_node[count].Index_up - 5)
		}
	}

	fmt.Println("awatin signal & MSG")

	c := make(chan os.Signal, 1)

	cc := make(chan struct{}) // канал с пустой структурой минимального размера для остановки горутин

	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGHUP)
	//    signal.Notify( c, os.Interrupt, syscall.SIGTERM)

	// для начала просмотрим все serial каналы обмена и просмотрим на них Coils и Holding Registrs
	for i := 0; i < config.Count_tty_serial; i++ {
		icc := make(chan inc_req, 3) // создадим канал для записи данных в горутину
		for y := 0; y < int(config.Tty_serial[i].Count_node); y++ {
			cmd := config.Tty_serial[i].Set_node[y].Command
			if cmd == 1 { // команда чтения Coils
				indx := config.Tty_serial[i].Set_node[y].Index_up // стартовый индекс
				id := uint8(config.Tty_serial[i].Set_node[y].Address_id)
				addr := config.Tty_serial[i].Set_node[y].Address_data
				for x := 0; x < int(config.Tty_serial[i].Set_node[y].Data_length); x++ {
					CoilsRed[int(indx)+x].ChanelSerial = i                        // на каком номере канала висит нода - можно не использовать связка пр каналу
					CoilsRed[int(indx)+x].ChanelTCP = 0                           // у нас канал SERIAL, не TCP
					CoilsRed[int(indx)+x].Address_data = uint16(addr) + uint16(x) // связанный адрес в устройстве
					CoilsRed[int(indx)+x].Address_id = id                         // адрес устройства
					CoilsRed[int(indx)+x].icc = icc                               // канал для передачи данных в горутину
				}
			}
			if cmd == 3 { // команда чтения Holding Registers
				indx := config.Tty_serial[i].Set_node[y].Index_up // стартовый индекс
				id := uint8(config.Tty_serial[i].Set_node[y].Address_id)
				addr := config.Tty_serial[i].Set_node[y].Address_data
				for x := 0; x < int(config.Tty_serial[i].Set_node[y].Data_length); x++ {
					HRegRed[int(indx)+x].ChanelSerial = i                        // на каком номере канала висит нода - можно не использовать связка пр каналу
					HRegRed[int(indx)+x].ChanelTCP = 0                           // у нас канал SERIAL, не TCP
					HRegRed[int(indx)+x].Address_data = uint16(addr) + uint16(x) // связанный адрес в устройстве
					HRegRed[int(indx)+x].Address_id = id                         // адрес устройства
					HRegRed[int(indx)+x].icc = icc                               // канал для передачи данных в горутину
				}
			}
		}
		// вызов циклической горутины запроса, пока одной по конкретному каналу (направлению опроса)
		go req_serial(serv, &config.Tty_serial[i], cc, icc) // запускаем обработчик канала ввода-вывода
	}

	// для начала просмотрим все TCP каналы обмена и просмотрим на них Coils и Holding Registrs
	for i := 0; i < config.Count_tcp_serial; i++ {
		icc := make(chan inc_req, 3) // создадим канал для записи данных в горутину
		for y := 0; y < int(config.Tcp_serial[i].Count_node); y++ {
			cmd := config.Tcp_serial[i].Set_node[y].Command
			if cmd == 1 { // команда чтения Coils
				indx := config.Tcp_serial[i].Set_node[y].Index_up // стартовый индекс
				id := uint8(config.Tcp_serial[i].Set_node[y].Address_id)
				addr := config.Tcp_serial[i].Set_node[y].Address_data
				for x := 0; x < int(config.Tcp_serial[i].Set_node[y].Data_length); x++ {
					// Можно делать проверку на перезапись данных по сериал, но пока не будем !!!!!
					CoilsRed[int(indx)+x].ChanelSerial = 0                        // на каком номере канала висит нода - можно не использовать связка пр каналу
					CoilsRed[int(indx)+x].ChanelTCP = i                           // у нас канал TCP, не SERIAL
					CoilsRed[int(indx)+x].Address_data = uint16(addr) + uint16(x) // связанный адрес в устройстве
					CoilsRed[int(indx)+x].Address_id = id                         // адрес устройства
					CoilsRed[int(indx)+x].icc = icc                               // канал для передачи данных в горутину
				}
			}
			if cmd == 3 { // команда чтения Holding Registers
				indx := config.Tcp_serial[i].Set_node[y].Index_up // стартовый индекс
				id := uint8(config.Tcp_serial[i].Set_node[y].Address_id)
				addr := config.Tcp_serial[i].Set_node[y].Address_data
				for x := 0; x < int(config.Tcp_serial[i].Set_node[y].Data_length); x++ {
					HRegRed[int(indx)+x].ChanelSerial = 0                        // на каком номере канала висит нода - можно не использовать связка пр каналу
					HRegRed[int(indx)+x].ChanelTCP = 1                           // у нас канал TCP, не SERIAL
					HRegRed[int(indx)+x].Address_data = uint16(addr) + uint16(x) // связанный адрес в устройстве
					HRegRed[int(indx)+x].Address_id = id                         // адрес устройства
					HRegRed[int(indx)+x].icc = icc                               // канал для передачи данных в горутину
				}
			}
		}
		// вызов циклической горутины запроса, пока одной по конкретному каналу (направлению опроса)
		go req_tcp_serial(serv, &config.Tcp_serial[i], cc, icc) // запускаем обработчик канала ввода-вывода
	}
	for {
		//	fmt.Println(serv)
		//fmt.Println(serv.HoldingRegisters[0])
		time.Sleep(time.Millisecond * 200) // 500 мл Сек опрос
	}
	<-c // ожидание нажатия Ctrl+C или kill

	cc <- struct{}{} // остановка горутин (минимальная версия по размеру )
	Debug = false    // останавливаем вывод на экран отладочной информации
	fmt.Printf("Stop !!!!!\n\n")
	Log.Printf("Stop !!!!!\n\n")
	time.Sleep(time.Millisecond * 500)
}
