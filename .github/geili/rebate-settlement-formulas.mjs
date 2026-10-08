// Geili rebate workbooks: J = cost, M = her points, O = my points.
// No runtime import here; the caller supplies a bundled artifact-tool workbook.
export function applyRebateSettlementFormulas(workbook, sheet, start, end) {
  const total = end + 1;
  for (let row = start; row <= end; row++) {
    sheet.getRange(`O${row}`).formulas = [[
      `=IF(COUNT(L${row},M${row})=2,L${row}-M${row},"")`,
    ]];
    sheet.getRange(`P${row}`).formulas = [[
      `=IF(COUNT(K${row},O${row})=2,K${row}*O${row},"")`,
    ]];
    sheet.getRange(`Q${row}`).formulas = [[
      `=IF(COUNT(L${row},M${row},O${row})=3,L${row}-M${row}-O${row},"")`,
    ]];
    sheet.getRange(`O${row}`).format.fill = row % 2 === start % 2 ? '#EFF5FA' : '#FFFFFF';
    sheet.getRange(`O${row}`).format.font.color = '#243447';
  }
  for (const column of ['O', 'P']) {
    sheet.getRange(`${column}${start}:${column}${end}`).conditionalFormats.add('cellIs', {
      operator: 'lessThan', formula: -0.00000001,
      format: { fill: '#FCE4D6', font: { color: '#9C0006', bold: true } },
    });
  }
  sheet.getRange('B12').formulas = [[
    `=IF(COUNT(P${start}:P${end})=${end-start+1},ROUND(SUM(P${start}:P${end}),2),"待填成本/她的点数")`,
  ]];
  sheet.getRange(`P${total}`).formulas = [[
    `=IF(COUNT(P${start}:P${end})=${end-start+1},SUM(P${start}:P${end}),"待填成本/她的点数")`,
  ]];
  sheet.getRange('D9').values = [['待补成本/她的点数（行）']];
  sheet.getRange('A15').values = [['黄色填写成本和她的点数；我的点数自动等于每刀差价减去她的点数。1 点 = 每个上游刀分 1 元。']];
  sheet.getRange(`A${total+2}`).values = [['应返她 = 折算用量 × 她的点数；我的点数 = 每刀差价 − 她的点数；我的分成 = 折算用量 × 我的点数。']];
  sheet.getRange(`A${total+3}`).values = [['每刀差价 = 售价 − 成本。缺成本或她的点数时，我的点数与分成留空；填 0 表示有效的零点数。']];
  sheet.getRange(`A${total+4}`).values = [['只填写黄色的成本和她的点数。我的点数为负表示返点超过差价，已标红。同一账号不同分组或售价已拆行。']];
  workbook.recalculate();
}
