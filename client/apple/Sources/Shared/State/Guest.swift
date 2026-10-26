import Foundation

/// Quem somos nas salas: sem login, um id e um nome aleatórios por arranque (o contrato deixa o userId livre).
struct Guest {
    let userId = "u" + String(UInt64.random(in: 0...UInt64.max), radix: 36).prefix(8)
    let name = "\(["Lontra", "Raposa", "Coruja", "Tucano", "Capivara", "Gaivota", "Tatu", "Jaguar", "Arara", "Sagui"].randomElement()!) \(Int.random(in: 10...99))"
    let hue = Double.random(in: 0..<360)
}
