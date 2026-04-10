import java.util.List;
import java.math.BigDecimal;
import java.math.RoundingMode;

/**
 * 价格计算工具类
 * Price Calculator Utility Class
 */
public class PriceCalculator {

    /**
     * 计算价格数组的平均值
     * @param prices 价格数组
     * @return 平均价格，保留2位小数
     */
    public static double calculateAveragePrice(double[] prices) {
        if (prices == null || prices.length == 0) {
            throw new IllegalArgumentException("价格数组不能为空");
        }
        
        double sum = 0.0;
        for (double price : prices) {
            if (price < 0) {
                throw new IllegalArgumentException("价格不能为负数");
            }
            sum += price;
        }
        
        return Math.round(sum / prices.length * 100.0) / 100.0;
    }

    /**
     * 计算价格列表的平均值
     * @param prices 价格列表
     * @return 平均价格，保留2位小数
     */
    public static double calculateAveragePrice(List<Double> prices) {
        if (prices == null || prices.isEmpty()) {
            throw new IllegalArgumentException("价格列表不能为空");
        }
        
        double sum = 0.0;
        for (Double price : prices) {
            if (price == null || price < 0) {
                throw new IllegalArgumentException("价格不能为null或负数");
            }
            sum += price;
        }
        
        return Math.round(sum / prices.size() * 100.0) / 100.0;
    }

    /**
     * 使用BigDecimal计算精确的平均价格（推荐用于金融计算）
     * @param prices 价格数组
     * @return 平均价格，保留2位小数
     */
    public static BigDecimal calculateAveragePricePrecise(BigDecimal[] prices) {
        if (prices == null || prices.length == 0) {
            throw new IllegalArgumentException("价格数组不能为空");
        }
        
        BigDecimal sum = BigDecimal.ZERO;
        for (BigDecimal price : prices) {
            if (price == null || price.compareTo(BigDecimal.ZERO) < 0) {
                throw new IllegalArgumentException("价格不能为null或负数");
            }
            sum = sum.add(price);
        }
        
        return sum.divide(BigDecimal.valueOf(prices.length), 2, RoundingMode.HALF_UP);
    }

    /**
     * 计算加权平均价格
     * @param prices 价格数组
     * @param weights 权重数组
     * @return 加权平均价格
     */
    public static double calculateWeightedAveragePrice(double[] prices, double[] weights) {
        if (prices == null || weights == null || prices.length == 0 || weights.length == 0) {
            throw new IllegalArgumentException("价格数组和权重数组不能为空");
        }
        
        if (prices.length != weights.length) {
            throw new IllegalArgumentException("价格数组和权重数组长度必须相同");
        }
        
        double weightedSum = 0.0;
        double totalWeight = 0.0;
        
        for (int i = 0; i < prices.length; i++) {
            if (prices[i] < 0 || weights[i] < 0) {
                throw new IllegalArgumentException("价格和权重都不能为负数");
            }
            weightedSum += prices[i] * weights[i];
            totalWeight += weights[i];
        }
        
        if (totalWeight == 0) {
            throw new IllegalArgumentException("权重总和不能为0");
        }
        
        return Math.round(weightedSum / totalWeight * 100.0) / 100.0;
    }

    // 示例用法
    public static void main(String[] args) {
        // 测试普通平均价格计算
        double[] prices1 = {10.50, 15.20, 8.99, 12.30, 20.00};
        System.out.println("普通平均价格: " + calculateAveragePrice(prices1));
        
        // 测试精确计算
        BigDecimal[] prices2 = {
            new BigDecimal("10.50"),
            new BigDecimal("15.20"),
            new BigDecimal("8.99"),
            new BigDecimal("12.30"),
            new BigDecimal("20.00")
        };
        System.out.println("精确平均价格: " + calculateAveragePricePrecise(prices2));
        
        // 测试加权平均价格
        double[] prices3 = {100.0, 200.0, 150.0};
        double[] weights = {0.3, 0.5, 0.2};
        System.out.println("加权平均价格: " + calculateWeightedAveragePrice(prices3, weights));
    }
}