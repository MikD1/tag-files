using Microsoft.EntityFrameworkCore;
using TagFilesService.Model;

namespace TagFilesService.Infrastructure;

public class QueryablePaginatedList<T>(List<T> items, int totalItems, int pageIndex, int totalPages)
    : PaginatedList<T>(items, totalItems, pageIndex, totalPages)
{
    public static PaginatedList<T> Create(IQueryable<T> source, int pageIndex, int pageSize)
    {
        int totalItems = source.Count();
        IQueryable<T> query = MakeQuery(source, pageIndex, pageSize);
        List<T> items = query.ToList();
        return new(items, totalItems, pageIndex, CalculateTotalPages(totalItems, pageSize));
    }

    public static async Task<PaginatedList<T>> CreateAsync(IQueryable<T> source, int pageIndex, int pageSize)
    {
        int totalItems = await source.CountAsync();
        IQueryable<T> query = MakeQuery(source, pageIndex, pageSize);
        List<T> items = await query.ToListAsync();
        return new(items, totalItems, pageIndex, CalculateTotalPages(totalItems, pageSize));
    }

    private static IQueryable<T> MakeQuery(IQueryable<T> source, int pageIndex, int pageSize)
    {
        return source.Skip((pageIndex - 1) * pageSize).Take(pageSize);
    }

    private static int CalculateTotalPages(int totalItems, int pageSize)
    {
        return (int)Math.Ceiling(totalItems / (double)pageSize);
    }
}